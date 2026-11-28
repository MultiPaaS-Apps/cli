package main

// The site commands publish a directory of static files as an application's site, served at an environment's site
// URL: the app's default site, which every environment without its own serves, or one environment's own site.

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/Khan/genqlient/graphql"
	"github.com/spf13/cobra"
)

// The limits the server enforces on a deployment (packages/core/src/site-limits.ts in the multipaas repo), checked
// here first so an oversized directory is refused before anything is sent. The SPA index limit applies with SPA
// fallback on, because the edge answers every missing path with that file. The path limit caps the UTF-8 bytes of
// every path together, because one answer carries every file's upload link.
const (
	siteMaxFiles          = 2000
	siteMaxTotalBytes     = 100 << 20
	siteMaxFileBytes      = 25 << 20
	siteMaxSPAIndexBytes  = 4 << 20
	siteMaxTotalPathBytes = 320 << 10
)

const (
	siteUploadParallel = 8
	siteUploadRetries  = 3
	sitePublishRetries = 3
)

var (
	sitePollInterval = 3 * time.Second
	siteWaitLimit    = 3 * time.Minute
	siteRetryDelay   = time.Second
)

func siteCommands(command commandFunc) *cobra.Command {
	return group("site", "Publish a directory of static files at an environment's site URL", siteDeploy(command), siteStatus(command), siteRemove(command))
}

func siteDeploy(command commandFunc) *cobra.Command {
	var appID, envID, dir string
	var noSPA, appSite bool
	cmd := command("deploy <dir>", "Publish a directory as the application's site, or with --env as that environment's own", func(ctx context.Context, s *session) (any, func(io.Writer), error) {
		files, err := siteFiles(dir, !noSPA)
		if err != nil {
			return nil, nil, err
		}
		scope, err := s.appScope(ctx, appID, "")
		if err != nil {
			return nil, nil, err
		}
		inputs := []SiteFileInput{}
		deployed := siteDeployment{FileCount: len(files)}
		for _, file := range files {
			inputs = append(inputs, SiteFileInput{Path: file.path, SizeBytes: int(file.size), Sha256: file.sha256})
			deployed.TotalBytes += file.size
		}
		var created *CreateSiteDeploymentResponse
		err = s.call(ctx, scope, func(client graphql.Client) (err error) {
			created, err = CreateSiteDeployment(ctx, client, appID, envID, inputs, !noSPA)
			return err
		})
		if err != nil {
			return nil, nil, err
		}
		deployed.DeploymentID = created.CreateSiteDeployment.DeploymentId
		if err := uploadSite(ctx, files, created.CreateSiteDeployment.Uploads); err != nil {
			return nil, nil, err
		}
		site, err := publishSite(ctx, s, scope, deployed.DeploymentID)
		if err != nil {
			return nil, nil, err
		}
		if deployed.SiteURL, err = servingURL(ctx, s, scope, site.Id, envID); err != nil {
			return nil, nil, unverified(deployed.DeploymentID, err)
		}
		if deployed.SiteURL != "" {
			fmt.Fprintf(s.env.stderr, "published deployment %s; waiting for %s to serve it, which takes about a minute\n", deployed.DeploymentID, deployed.SiteURL)
			if err := waitUntilServed(ctx, deployed.SiteURL, deployed.DeploymentID); err != nil {
				return nil, nil, err
			}
		}
		return deployed, deployed.print, nil
	})
	cmd.Long = "Uploads every file under the directory, following links that stay inside it, then publishes them as one " +
		"deployment and waits until the site URL serves it. It publishes the site of --env, else of multipaas.json's " +
		"environmentId for the same application, else the application's; that environment's own site is served by that " +
		"environment alone, and the application's by every environment that has no site of its own. --app-site picks " +
		"the application's whatever multipaas.json says. A deployment holds " +
		"at most 2000 files and 100 MiB, and a file at most 25 MiB. With SPA fallback, on unless --no-spa, a path the site " +
		"doesn't have serves the site's root index.html, which may then hold at most 4 MiB; without it, a 404 page.\n\n" +
		"An environment's own site needs environment.manage in that environment, and the application's site needs it in every environment. " +
		"siteUrl is empty in --json output when every environment has a site of its own, so none serves the application's."
	cmd.Args = oneID(&dir)
	appFlag(cmd, &appID)
	siteEnvFlag(cmd, &appID, &envID, &appSite)
	cmd.Flags().BoolVar(&noSPA, "no-spa", false, "answer a path the site doesn't have with a 404 page, not the root index.html")
	return cmd
}

func siteStatus(command commandFunc) *cobra.Command {
	var appID, envID string
	cmd := command("status", "Show the site an environment serves, its live deployment and its site URL", func(ctx context.Context, s *session) (any, func(io.Writer), error) {
		var status siteState
		err := s.inApp(ctx, appID, func(client graphql.Client) error {
			application, err := siteApplication(ctx, client)
			if err != nil {
				return err
			}
			for _, e := range application.Environments {
				if e.Id == envID || (envID == "" && e.IsDefault) {
					status = siteStateOf(application, e)
					return nil
				}
			}
			return &cliError{exit: exitRefused, Code: "NOT_FOUND", Message: "environment " + envID + " is not an environment of application " + appID}
		})
		return status, status.print, err
	})
	appFlag(cmd, &appID)
	envFlagFromProject(cmd, &appID, &envID, nil)
	return cmd
}

func siteRemove(command commandFunc) *cobra.Command {
	var appID, envID string
	var yes, appSite bool
	cmd := command("remove", "Remove the site of --env, else of multipaas.json's environmentId, else delete the application's site, which needs --yes", func(ctx context.Context, s *session) (any, func(io.Writer), error) {
		if envID == "" && !yes {
			return nil, nil, &cliError{exit: exitRefused, Code: "CONFIRMATION_REQUIRED", Message: "deleting the application's site takes it offline in every environment without a site of its own"}
		}
		var removed removal
		err := s.inApp(ctx, appID, func(client graphql.Client) error {
			if envID != "" {
				response, err := RemoveEnvironmentSite(ctx, client, envID)
				if err == nil {
					removed = removal{Removed: true, ID: response.RemoveEnvironmentSite.Id}
				}
				return err
			}
			response, err := DeleteApplicationSite(ctx, client, appID)
			if err == nil {
				removed = removal{Removed: true, ID: response.DeleteApplicationSite.Id}
			}
			return err
		})
		return removed, func(out io.Writer) {
			if envID != "" {
				fmt.Fprintf(out, "environment %s has no site of its own now; it serves the application's site, if there is one\n", removed.ID)
				return
			}
			fmt.Fprintf(out, "application %s has no site now; only environments with a site of their own serve one\n", removed.ID)
		}, err
	})
	cmd.Long = "With --env, or multipaas.json's environmentId, removes that environment's own site, and the environment " +
		"serves the application's site again. Without either, or with --app-site, deletes the application's site, which every environment without a site of its own stops serving, " +
		"so it needs --yes. Both need environment.manage, as site deploy does. Running it again after it succeeded, or " +
		"after SITE_ROUTES_PENDING, succeeds too; it is refused with NOT_FOUND where there never was a site, for an environment that isn't the application's or is being removed, and for an application you can't reach (deleted, not one you have access to, or, as a deployer, not the deployer's own)."
	appFlag(cmd, &appID)
	siteEnvFlag(cmd, &appID, &envID, &appSite)
	cmd.Flags().BoolVar(&yes, "yes", false, "delete the application's site")
	return cmd
}

// siteEnvFlag is --env for a command that acts on a site, an environment's own, defaulting to multipaas.json's
// environmentId as its siblings' --env does. Without either the command acts on the application's site, which
// --app-site picks whatever multipaas.json says.
func siteEnvFlag(cmd *cobra.Command, appID, envID *string, appSite *bool) {
	envFlagFromProject(cmd, appID, envID, func() bool { return *appSite })
	cmd.Flags().Lookup("env").Usage = "the environment whose own site to act on (from envs list; default: multipaas.json's environmentId for the same application, else the application's site, which every environment without its own serves)"
	cmd.Flags().BoolVar(appSite, "app-site", false, "act on the application's site even when multipaas.json names an environment")
	cmd.MarkFlagsMutuallyExclusive("env", "app-site")
}

// siteFile is one file to publish: its path in the site, where it is on disk, and its size and SHA-256.
type siteFile struct {
	path, local, sha256 string
	size                int64
}

// siteFiles lists every file under dir with its size and SHA-256. It follows links that stay inside dir and refuses
// the directory, before anything is sent, when a link can't be followed or leaves it, or a limit would be passed.
// spa adds the SPA index limit.
func siteFiles(dir string, spa bool) ([]siteFile, error) {
	info, err := os.Stat(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, &cliError{exit: exitRefused, Code: "SITE_DIR_INVALID", Message: dir + " doesn't exist"}
	}
	if err != nil {
		return nil, err
	}
	if !info.IsDir() {
		return nil, &cliError{exit: exitRefused, Code: "SITE_DIR_INVALID", Message: dir + " is not a directory"}
	}
	absolute, err := filepath.Abs(dir)
	if err != nil {
		return nil, err
	}
	root, err := filepath.EvalSymlinks(absolute)
	if err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(absolute)
	if err != nil {
		return nil, err
	}
	walk := &fileWalk{dir: absolute, inside: root, found: []string{}}
	for _, entry := range entries {
		if err := walk.visit(entry.Name(), []string{root}); err != nil {
			return nil, err
		}
	}
	if problems := walk.problems(); len(problems) > 0 {
		return nil, &cliError{exit: exitRefused, Code: "SITE_DIR_INVALID", Message: strings.Join(append(problems, "Nothing was uploaded."), " ")}
	}
	if len(walk.found) == 0 {
		return nil, &cliError{exit: exitRefused, Code: "SITE_DIR_INVALID", Message: "no files in " + dir}
	}
	for _, rel := range walk.found {
		if strings.ContainsAny(rel, `"\`) || strings.ContainsFunc(rel, func(r rune) bool { return r < 0x20 || r == 0x7f }) {
			return nil, &cliError{exit: exitRefused, Code: "SITE_PATH_INVALID", Path: rel, Message: fmt.Sprintf(
				"%q can't be a site path: it holds a double quote, a backslash or a control character. Nothing was uploaded.", rel)}
		}
		if !utf8.ValidString(rel) {
			return nil, &cliError{exit: exitRefused, Code: "SITE_PATH_INVALID", Path: rel, Message: fmt.Sprintf(
				"%q can't be a site path: it isn't valid UTF-8. Nothing was uploaded.", rel)}
		}
	}
	if len(walk.found) > siteMaxFiles {
		return nil, siteLimit("files", siteMaxFiles, int64(len(walk.found)), "")
	}
	var pathBytes int64
	for _, rel := range walk.found {
		pathBytes += int64(len(rel))
	}
	if pathBytes > siteMaxTotalPathBytes {
		return nil, siteLimit("pathBytes", siteMaxTotalPathBytes, pathBytes, "")
	}
	files := []siteFile{}
	var total int64
	for _, rel := range walk.found {
		local := filepath.Join(dir, filepath.FromSlash(rel))
		info, err := os.Stat(local)
		if err != nil {
			return nil, err
		}
		if info.Size() > siteMaxFileBytes {
			return nil, siteLimit("fileBytes", siteMaxFileBytes, info.Size(), rel)
		}
		if spa && rel == "index.html" && info.Size() > siteMaxSPAIndexBytes {
			return nil, siteLimit("spaIndexBytes", siteMaxSPAIndexBytes, info.Size(), rel)
		}
		total += info.Size()
		files = append(files, siteFile{path: rel, local: local})
	}
	if total > siteMaxTotalBytes {
		return nil, siteLimit("totalBytes", siteMaxTotalBytes, total, "")
	}
	for i := range files {
		if files[i].size, files[i].sha256, err = fileDigest(files[i].local); err != nil {
			return nil, err
		}
	}
	return files, nil
}

// siteLimit is the refusal the server would send for a limit, given before anything is sent.
func siteLimit(limit string, most, actual int64, path string) error {
	rule := map[string][2]string{
		"files":         {"files", "a deployment holds"},
		"totalBytes":    {"bytes in all", "a deployment holds"},
		"fileBytes":     {"bytes", "a file holds"},
		"spaIndexBytes": {"bytes", "with SPA fallback, the root index.html holds"},
		"pathBytes":     {"bytes of file paths", "a deployment's paths hold"},
	}[limit]
	subject := "the directory"
	if path != "" {
		subject = path
	}
	return &cliError{exit: exitRefused, Code: "SITE_LIMIT_EXCEEDED", Limit: limit, Max: most, Actual: actual, Path: path,
		Message: fmt.Sprintf("%s holds %d %s; %s at most %d. Nothing was uploaded.", subject, actual, rule[0], rule[1], most)}
}

// uploadSite PUTs every file to its presigned URL, siteUploadParallel at a time, and stops at the first that fails.
func uploadSite(ctx context.Context, files []siteFile, uploads []SiteUploadParts) error {
	byPath := map[string]siteFile{}
	for _, file := range files {
		byPath[file.path] = file
	}
	for _, upload := range uploads {
		if _, ok := byPath[upload.Path]; !ok {
			return &cliError{exit: exitRefused, Code: "CLI_ERROR", Message: "the server asked for " + upload.Path + ", which is not in the directory. Nothing was published."}
		}
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	slots := make(chan struct{}, siteUploadParallel)
	var wg sync.WaitGroup
	var once sync.Once
	var failed error
	for _, upload := range uploads {
		slots <- struct{}{}
		if ctx.Err() != nil {
			break
		}
		wg.Go(func() {
			defer func() { <-slots }()
			if err := putSiteFile(ctx, byPath[upload.Path], upload); err != nil {
				once.Do(func() { failed = err; cancel() })
			}
		})
	}
	wg.Wait()
	return failed
}

// putSiteFile PUTs one file with exactly the headers the server signed, again up to siteUploadRetries times after a
// 5xx or no response. Any other answer, such as an expired signature, is final.
func putSiteFile(ctx context.Context, file siteFile, upload SiteUploadParts) error {
	status, answer := 0, ""
	for attempt := 0; attempt <= siteUploadRetries; attempt++ {
		if attempt > 0 {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(siteRetryDelay):
			}
		}
		var err error
		status, answer, err = put(ctx, file.local, file.size, upload.Url, upload.Headers)
		if err == nil && status >= 200 && status < 300 {
			return nil
		}
		var unreadable localFileError
		if errors.As(err, &unreadable) {
			return &cliError{exit: exitRefused, Code: "CLI_ERROR", Message: fmt.Sprintf(
				"upload of %s failed: the file can't be read (%v); nothing was published. Run site deploy again.", file.path, unreadable.error)}
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if status != 0 && status < 500 {
			break
		}
	}
	if status == http.StatusForbidden && strings.Contains(answer, "Request has expired") {
		return &cliError{exit: exitRefused, Code: "UPLOAD_REFUSED", Message: fmt.Sprintf(
			"upload of %s refused (HTTP 403): its upload link expired, 15 minutes after site deploy asked for it; nothing was published. Run site deploy again, which asks for new links.", file.path)}
	}
	reason := "no response"
	if status != 0 {
		reason = fmt.Sprintf("HTTP %d", status)
	}
	return &cliError{exit: exitRefused, Code: "UPLOAD_REFUSED", Message: fmt.Sprintf("upload of %s refused (%s); nothing was published. Run site deploy again.", file.path, reason)}
}

// publishSite publishes the deployment, asking again up to sitePublishRetries times while publishRetryable says to.
func publishSite(ctx context.Context, s *session, scope, deploymentID string) (SiteParts, error) {
	for attempt := 0; ; attempt++ {
		var published *PublishSiteDeploymentResponse
		err := s.call(ctx, scope, func(client graphql.Client) (err error) {
			published, err = PublishSiteDeployment(ctx, client, deploymentID)
			return err
		})
		if err == nil {
			return published.PublishSiteDeployment, nil
		}
		if attempt == sitePublishRetries || !publishRetryable(ctx, err) {
			return SiteParts{}, routesPending(deploymentID, err)
		}
		select {
		case <-ctx.Done():
			return SiteParts{}, ctx.Err()
		case <-time.After(siteRetryDelay):
		}
	}
}

// publishRetryable reports whether a publish that failed with err is worth asking again: its route write is pending,
// or it got no answer, a 5xx, or INTERNAL_SERVER_ERROR, which is how the API answers a resolver that failed: HTTP 200
// with data null and the masked message "Unexpected error.". A publish is idempotent: asking again for one that
// landed only writes its routes again.
func publishRetryable(ctx context.Context, err error) bool {
	if ctx.Err() != nil {
		return false
	}
	var refusal *cliError
	var failed *graphql.HTTPError
	var unanswered *url.Error
	switch {
	case errors.As(err, &refusal):
		return refusal.Code == "SITE_ROUTES_PENDING" || refusal.Code == "INTERNAL_SERVER_ERROR"
	case errors.As(err, &failed):
		return failed.StatusCode >= 500
	default:
		return errors.As(err, &unanswered)
	}
}

// routesPending is err with the deployment id in its message when err is SITE_ROUTES_PENDING: the deployment is
// already the site's active one, and only the site address isn't updated yet. Any other err is returned as it is.
func routesPending(deploymentID string, err error) error {
	var refusal *cliError
	if !errors.As(err, &refusal) || refusal.Code != "SITE_ROUTES_PENDING" {
		return err
	}
	failure := *refusal
	failure.cause = err
	failure.Message = fmt.Sprintf("deployment %s is published, but its routes aren't written yet: %s", deploymentID, refusal.Message)
	return &failure
}

// unverified is err, a failure after the deployment was published, with a message that says so and keeps the
// deployment id. It keeps err's code and details, and err as its cause.
func unverified(deploymentID string, err error) error {
	failure := *asCLIError(err)
	failure.cause = err
	failure.Message = fmt.Sprintf("deployment %s is published, but it could not be verified live: %s", deploymentID, failure.Message)
	return &failure
}

// servingURL is the site URL that serves siteID. With envID it is that environment's, or an error when the
// environment isn't there or doesn't serve the site. Without, it is the default environment's, else the first
// environment's that serves it, and "" when none does, because every environment has a site of its own.
func servingURL(ctx context.Context, s *session, scope, siteID, envID string) (string, error) {
	var application SitesMeUserApplication
	err := s.call(ctx, scope, func(client graphql.Client) (err error) {
		application, err = siteApplication(ctx, client)
		return err
	})
	if err != nil {
		return "", err
	}
	found := ""
	for _, e := range application.Environments {
		serves := e.ServedSite != nil && e.ServedSite.Id == siteID
		switch {
		case envID != "":
			if e.Id != envID {
				continue
			}
			if !serves {
				return "", &cliError{exit: exitRefused, Code: "CLI_ERROR", Message: "environment " + envID + " doesn't serve the site that was published"}
			}
			return e.SiteUrl, nil
		case serves && e.IsDefault:
			return e.SiteUrl, nil
		case serves && found == "":
			found = e.SiteUrl
		}
	}
	if envID != "" {
		return "", &cliError{exit: exitRefused, Code: "NOT_FOUND", Message: "environment " + envID + " is not an environment of application " + application.Id}
	}
	unserved := slices.ContainsFunc(application.Environments, func(e SitesMeUserApplicationEnvironmentsEnvironment) bool {
		return e.ServedSite == nil && (e.Site == nil || !e.Site.Disabled)
	})
	if found == "" && unserved && siteDisabled(application) {
		return "", &cliError{exit: exitRefused, Code: "SITE_DISABLED", Message: "MultiPaaS has disabled a site of this application, so no environment serves the site that was published"}
	}
	return found, nil
}

// waitUntilServed polls siteURL's /.multipaas/deployment until it answers deploymentID: a published route takes about
// a minute to reach every edge location.
func waitUntilServed(ctx context.Context, siteURL, deploymentID string) error {
	deadline := time.Now().Add(siteWaitLimit)
	polling, cancel := context.WithDeadline(ctx, deadline)
	defer cancel()
	for !servesDeployment(polling, siteURL, deploymentID) {
		if time.Now().After(deadline) {
			return &cliError{exit: exitRefused, Code: "SITE_NOT_LIVE_YET", Message: fmt.Sprintf(
				"deployment %s is published, but %s did not serve it within %s.", deploymentID, siteURL, siteWaitLimit)}
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(sitePollInterval):
		}
	}
	return nil
}

// servesDeployment reports whether siteURL answers deploymentID as the deployment it serves. Any failure reads as not yet.
func servesDeployment(ctx context.Context, siteURL, deploymentID string) bool {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, siteURL+"/.multipaas/deployment", nil)
	if err != nil {
		return false
	}
	response, err := transferClient().Do(request)
	if err != nil {
		return false
	}
	defer response.Body.Close()
	body, err := io.ReadAll(io.LimitReader(response.Body, 256))
	return err == nil && response.StatusCode == http.StatusOK && strings.TrimSpace(string(body)) == deploymentID
}

// siteDeployment is what site deploy prints.
type siteDeployment struct {
	DeploymentID string `json:"deploymentId"`
	SiteURL      string `json:"siteUrl"`
	FileCount    int    `json:"fileCount"`
	TotalBytes   int64  `json:"totalBytes"`
}

func (d siteDeployment) print(out io.Writer) {
	if d.SiteURL == "" {
		fmt.Fprintf(out, "deployment %s is published (%d files, %d bytes), but every environment has a site of its own, so none serves it\n", d.DeploymentID, d.FileCount, d.TotalBytes)
		return
	}
	fmt.Fprintf(out, "deployment %s is live at %s (%d files, %d bytes)\n", d.DeploymentID, d.SiteURL, d.FileCount, d.TotalBytes)
}

// siteState is what site status prints. Source says whose site the environment serves: "environment" for its own,
// "application" for the application's, "none" when it serves no site. Disabled says MultiPaaS has disabled a site of
// the application, so none of its sites can be published or removed.
type siteState struct {
	EnvironmentID string     `json:"environmentId"`
	SiteURL       string     `json:"siteUrl"`
	Source        string     `json:"source"`
	Site          *SiteParts `json:"site"`
	Disabled      bool       `json:"disabled"`
}

// siteDisabled is whether MultiPaaS has disabled a site of the application: its own or any environment's.
func siteDisabled(application SitesMeUserApplication) bool {
	disabled := application.Site != nil && application.Site.Disabled
	for _, e := range application.Environments {
		disabled = disabled || (e.Site != nil && e.Site.Disabled)
	}
	return disabled
}

func siteStateOf(application SitesMeUserApplication, e SitesMeUserApplicationEnvironmentsEnvironment) siteState {
	status := siteState{EnvironmentID: e.Id, SiteURL: e.SiteUrl, Source: "none", Site: e.ServedSite}
	status.Disabled = siteDisabled(application)
	switch {
	case e.ServedSite != nil && e.Site != nil && e.Site.Id == e.ServedSite.Id:
		status.Source = "environment"
	case e.ServedSite != nil:
		status.Source = "application"
	}
	return status
}

func (s siteState) print(out io.Writer) {
	switch {
	case s.Site == nil && s.Disabled:
		fmt.Fprintf(out, "environment %s serves no site at %s; MultiPaaS has disabled a site of this application, so none can be published. Ask MultiPaaS why with multipaas feedback send\n", s.EnvironmentID, s.SiteURL)
		return
	case s.Site == nil:
		fmt.Fprintf(out, "environment %s serves no site at %s; publish one with site deploy\n", s.EnvironmentID, s.SiteURL)
		return
	}
	fmt.Fprintf(out, "environment %s serves the %s's site %s at %s\n", s.EnvironmentID, s.Source, s.Site.Id, s.SiteURL)
	if d := s.Site.ActiveDeployment; d != nil {
		published := ""
		if d.PublishedAt != nil {
			published = ", published " + *d.PublishedAt
		}
		fmt.Fprintf(out, "deployment %s %s, %d files, %.0f bytes%s\n", d.Id, d.Status, d.FileCount, d.TotalBytes, published)
	}
	if s.Disabled {
		fmt.Fprintln(out, "MultiPaaS has disabled a site of this application, so none of its sites can be published. Ask MultiPaaS why with multipaas feedback send")
	}
}

func siteApplication(ctx context.Context, client graphql.Client) (SitesMeUserApplication, error) {
	response, err := Sites(ctx, client)
	if err != nil {
		return SitesMeUserApplication{}, err
	}
	return response.Me.Application, nil
}

// siteHints are the hints for a site command's refusal: what to change, and what to run next.
func (e *cliError) siteHints() []string {
	switch e.Code {
	case "SITE_LIMIT_EXCEEDED":
		if e.Limit == "pathBytes" {
			return []string{"A deployment's file paths may total at most 320 KiB, because one answer carries every file's upload link. Shorten long paths, or leave out files the site doesn't serve, then run site deploy again."}
		}
		if e.Limit == "spaIndexBytes" {
			return []string{"With SPA fallback the root index.html answers every path the site doesn't have, so it may hold at most 4 MiB. Make it smaller, or run site deploy again with --no-spa."}
		}
		return []string{"A deployment holds at most 2000 files and 100 MiB, and a file at most 25 MiB. Leave out what the site doesn't serve, such as source maps, then run site deploy again."}
	case "SITE_PATH_INVALID":
		return []string{fmt.Sprintf("A site can't hold %q: .multipaas/ is reserved, and a path can't be too long, hold a double quote, a backslash or a control character, or be other than valid UTF-8. Rename or remove it, then run site deploy again.", e.Path)}
	case "SITE_UPLOAD_INCOMPLETE":
		hints := []string{}
		if len(e.Missing) > 0 {
			hints = append(hints, "Not uploaded: "+strings.Join(e.Missing, ", ")+".")
		}
		if len(e.Mismatched) > 0 {
			hints = append(hints, "Changed after site deploy read them: "+strings.Join(e.Mismatched, ", ")+".")
		}
		return append(hints, "Nothing was published. Run site deploy again once the directory stops changing.")
	case "SITE_ROUTES_PENDING":
		if e.command == "site remove" {
			return []string{"The site is removed, but the site URL doesn't show it yet. Run the same site remove again, or wait for the daily reconcile to repair it."}
		}
		return []string{"The deployment is published, but the site URL doesn't serve it yet. Run site deploy again, or wait for the daily reconcile to repair it."}
	case "INTERNAL_SERVER_ERROR":
		switch e.command {
		case "site status":
			return []string{"Something failed on the server. Run site status again."}
		case "site remove":
			return []string{"Something failed on the server. Run site remove again; removing a site that is already gone succeeds."}
		}
		return []string{"Something failed on the server. Run site deploy again; publishing is safe to repeat."}
	case "SITE_DISABLED":
		return []string{"MultiPaaS has disabled a site of this application, so none of its sites can be published or removed. Ask MultiPaaS why with multipaas feedback send."}
	case "SITE_DEPLOYMENT_NOT_PUBLISHABLE":
		return []string{"Another publish replaced this deployment, or it waited too long to be published. Run site deploy again, which makes a new one."}
	case "SITE_NOT_LIVE_YET":
		return []string{"The deployment is published. Run site status in a few minutes; if the site URL still doesn't serve it, run site deploy again."}
	case "NOT_FOUND":
		if e.command == "site remove" {
			return []string{"There is no site to remove there, the environment isn't one of the application's or is being removed, or the application isn't one you can reach. Check --app against apps list and --env against envs list; site status shows what an environment serves."}
		}
		return []string{"Check --app against apps list, and --env against envs list --app APP_ID."}
	case "UNAUTHORIZED":
		if e.command == "site status" {
			return []string{"Your access to the application was removed, or it was deleted, while the command ran. Check --app against apps list."}
		}
		return []string{"Publishing or removing a site needs environment.manage in the environment for its own site, and environment.manage in every environment for the application's; a deployer can do neither. If you had it, your access to the application was removed, or it was deleted, while the command ran: check --app against apps list."}
	case "CONFIRMATION_REQUIRED":
		return []string{"Run the command again with --yes to delete it, or with --env to remove one environment's own site."}
	}
	return nil
}
