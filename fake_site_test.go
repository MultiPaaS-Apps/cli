package main

import (
	"cmp"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"io"
	"maps"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
)

// siteFake is a signed-in fake whose site operations run against a stand-in for S3 and the sites edge.
func siteFake(t *testing.T) *fakeMultiPaaS {
	fake := signedInFake(t, fresh)
	fake.sites = newFakeSites(t)
	interval, limit, delay := sitePollInterval, siteWaitLimit, siteRetryDelay
	sitePollInterval, siteRetryDelay = time.Millisecond, 0
	t.Cleanup(func() { sitePollInterval, siteWaitLimit, siteRetryDelay = interval, limit, delay })
	return fake
}

// siteRefusalMessages are the live server's messages for the site refusals that have one message
// (packages/services/src/sites/errors.ts, and UnauthorizedError, in the multipaas repo). NOT_FOUND's message names
// what was not found, so each NOT_FOUND below spells out NotFoundError's "<type> not found".
var siteRefusalMessages = map[string]string{
	"SITE_LIMIT_EXCEEDED":             "This deployment is over a site limit.",
	"SITE_UPLOAD_INCOMPLETE":          "Some files weren't uploaded, or don't match what the deployment declared.",
	"SITE_PATH_INVALID":               "That file path isn't allowed in a site.",
	"SITE_ROUTES_PENDING":             "The change is saved, but the site address isn't updated yet. Try again.",
	"SITE_DISABLED":                   "This site has been disabled by MultiPaaS. Contact support.",
	"SITE_DEPLOYMENT_NOT_PUBLISHABLE": "That deployment can't be published. Create a new one.",
	"UNAUTHORIZED":                    "unauthorized",
	"INTERNAL_SERVER_ERROR":           "Unexpected error.",
}

// fakeContentTypes are the server's content types for the extensions the tests use (packages/core/src/content-type.ts
// in the multipaas repo); any other file is application/octet-stream, as there.
var fakeContentTypes = map[string]string{
	".html": "text/html; charset=utf-8",
	".js":   "text/javascript; charset=utf-8",
	".txt":  "text/plain; charset=utf-8",
}

// fakeSites holds the site operations' state, and an httptest server standing in for S3 and the sites edge. A
// presigned PUT lands in objects, refused as S3 refuses one without the headers it was signed with, or with a body
// whose length or SHA-256 is not the one signed. A GET of /<label>/.multipaas/deployment answers the deployment the label's route
// serves, once the route has propagated: a site URL in the fake is the server's URL followed by the label.
type fakeSites struct {
	server *httptest.Server

	mu               sync.Mutex
	objects          map[string]fakeUpload         // "<deployment id>/<path>" -> what its PUT received
	signed           map[string]map[string]string  // "<deployment id>/<path>" -> the headers its upload was signed with
	sizes            map[string]int64              // "<deployment id>/<path>" -> the content-length its upload was signed with
	attempts         map[string]int                // path -> PUTs received for it
	failPuts         map[string]int                // path -> how many more of its PUTs answer failStatus
	failStatus       int                           // what a failing PUT answers: 503 unless set
	failBody         string                        // the body a failing PUT answers: SlowDown unless set
	dropPut          string                        // a PUT of this path answers 200 but stores nothing
	putDelay         time.Duration                 // how long each PUT takes to answer
	inFlight, peak   int                           // PUTs being answered now, and the most at once
	siteIDs          map[string]string             // "app:<id>" or "env:<id>" -> that site's id
	removed          map[string]bool               // "app:<id>" or "env:<id>" whose latest site was removed
	live             map[string]string             // site id -> its live deployment's id
	deployments      map[string]fakeSiteDeployment // deployment id -> what it was created with
	routes           map[string]string             // label -> the deployment its route serves
	previous         map[string]string             // label -> the deployment its route served before it last changed
	stale            map[string]int                // label -> GETs that still answer the previous route
	propagation      int                           // GETs a changed route answers with its previous deployment first
	polls            int                           // GETs of /.multipaas/deployment answered
	pollStatus       int                           // the HTTP status a served /.multipaas/deployment answers with, or 0 for 200
	askFor           string                        // a path CreateSiteDeployment also asks to be uploaded, though it wasn't sent
	routesPending    int                           // site changes that commit but answer SITE_ROUTES_PENDING before one writes its routes
	publishes        int                           // PublishSiteDeployment requests received
	failPublishes    []int                         // what the next PublishSiteDeployment requests answer, before they publish: a status, 0 for no answer, or maskedPublish
	disabled         map[string]bool               // site id -> MultiPaaS has disabled it
	deletedApps      map[string]bool               // application id -> deleted after the CLI resolved its scope, so the server refuses its scope with UNAUTHORIZED
	refuse           map[string]map[string]any     // operation -> the refusal it answers: "code", and any extensions
	afterCreate      func()                        // runs when CreateSiteDeployment has made a deployment, before it answers
	afterPublish     func()                        // runs when a publish has made its deployment live, before it syncs routes
	disableOnPublish bool                          // a publish that lands leaves the site disabled, so no environment serves it
	minted           int                           // sites and deployments created so far; ids are never reused
}

// maskedPublish in failPublishes is the answer the API gives a resolver that failed: HTTP 200, data null, and graphql-yoga's
// masked "Unexpected error." with extensions code INTERNAL_SERVER_ERROR and unexpected true (graphql-yoga's maskError, which
// the multipaas API's maskUnexpectedError calls).
const maskedPublish = -1

// publishCount is the PublishSiteDeployment requests received so far.
func (s *fakeSites) publishCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.publishes
}

// fakeSiteDeployment is one deployment's site, and each file's SHA-256 and size as createSiteDeployment was sent them.
type fakeSiteDeployment struct {
	site   string
	sha256 map[string]string
	bytes  float64
}

func newFakeSites(t *testing.T) *fakeSites {
	sites := &fakeSites{
		objects: map[string]fakeUpload{}, signed: map[string]map[string]string{}, sizes: map[string]int64{}, attempts: map[string]int{}, failPuts: map[string]int{},
		siteIDs: map[string]string{}, removed: map[string]bool{}, live: map[string]string{}, deployments: map[string]fakeSiteDeployment{},
		routes: map[string]string{}, previous: map[string]string{}, stale: map[string]int{}, refuse: map[string]map[string]any{}, disabled: map[string]bool{}, deletedApps: map[string]bool{},
	}
	mux := http.NewServeMux()
	mux.HandleFunc("PUT /s3/{deployment}/{path...}", sites.receive)
	mux.HandleFunc("GET /{label}/.multipaas/deployment", sites.serve)
	sites.server = httptest.NewServer(mux)
	t.Cleanup(sites.server.Close)
	return sites
}

// manageSites serves the site operations in the scope's application. A publish checks every object as the server's
// HEADs do and makes the deployment live; a remove or delete takes the site away. Each then writes the routes, except
// while routesPending lasts, when it answers SITE_ROUTES_PENDING with its change made, as the server does when its
// route write fails. A second remove of a site already removed succeeds; one where there never was a site is NOT_FOUND.
func (f *fakeMultiPaaS) manageSites(w http.ResponseWriter, operation string, parts []string, variables map[string]any) {
	client, app := parts[0], appOf(parts)
	if app == "" {
		refuseSite(w, "UNAUTHORIZED", nil)
		return
	}
	if operation != "Sites" {
		f.mutations = append(f.mutations, variables)
	}
	s := f.sites
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.deletedApps[app] {
		refuseSite(w, "UNAUTHORIZED", nil) // the scope check refuses a deleted application before any resolver runs
		return
	}
	if operation == "PublishSiteDeployment" {
		s.publishes++
		if len(s.failPublishes) > 0 {
			status := s.failPublishes[0]
			s.failPublishes = s.failPublishes[1:]
			if status == 0 {
				conn, _, _ := w.(http.Hijacker).Hijack()
				_ = conn.Close()
				return
			}
			if status == maskedPublish {
				graphQLRefusalWith(w, "INTERNAL_SERVER_ERROR", "Unexpected error.", map[string]any{"unexpected": true})
				return
			}
			http.Error(w, http.StatusText(status), status)
			return
		}
	}
	if refusal, ok := s.refuse[operation]; ok {
		code, _ := refusal["code"].(string)
		refuseSite(w, code, refusal)
		return
	}
	switch operation {
	case "Sites":
		environments := []map[string]any{}
		for _, e := range f.environments[app] {
			environments = append(environments, map[string]any{
				"id": e.id, "isDefault": e.isDefault, "siteUrl": s.server.URL + "/" + f.siteLabel(client, app, e),
				"site": s.view(s.siteIDs["env:"+e.id]), "servedSite": s.view(s.served(app, e.id)),
			})
		}
		application := map[string]any{"id": app, "site": s.view(s.siteIDs["app:"+app]), "environments": environments}
		writeData(w, map[string]any{"me": map[string]any{"application": application}})
	case "CreateSiteDeployment":
		key := "app:" + app
		if env, ok := variables["environmentId"].(string); ok {
			if !f.hasEnvironment(app, env) {
				graphQLRefusal(w, "NOT_FOUND", "environment not found")
				return
			}
			key = "env:" + env
		}
		if s.siteIDs[key] == "" {
			s.minted++
			s.siteIDs[key] = fmt.Sprintf("site-%d", s.minted)
		}
		s.minted++
		id := fmt.Sprintf("dep-%d", s.minted)
		deployment := fakeSiteDeployment{site: s.siteIDs[key], sha256: map[string]string{}}
		uploads := []map[string]any{}
		files, _ := variables["files"].([]any)
		for _, item := range files {
			file, _ := item.(map[string]any)
			path, _ := file["path"].(string)
			digest, _ := file["sha256"].(string)
			size, _ := file["sizeBytes"].(float64)
			deployment.sha256[path] = digest
			deployment.bytes += size
			s.signed[id+"/"+path] = signedSiteHeaders(path, digest)
			s.sizes[id+"/"+path] = int64(size)
			headers := []map[string]any{}
			for _, name := range slices.Sorted(maps.Keys(s.signed[id+"/"+path])) {
				headers = append(headers, map[string]any{"name": name, "value": s.signed[id+"/"+path][name]})
			}
			uploads = append(uploads, map[string]any{"path": path, "url": s.uploadURL(id, path), "headers": headers})
		}
		if s.askFor != "" {
			uploads = append(uploads, map[string]any{"path": s.askFor, "url": s.uploadURL(id, s.askFor), "headers": []map[string]any{}})
		}
		s.deployments[id] = deployment
		if s.afterCreate != nil {
			s.afterCreate()
		}
		writeData(w, map[string]any{"createSiteDeployment": map[string]any{"deploymentId": id, "uploads": uploads}})
	case "PublishSiteDeployment":
		id, _ := variables["deploymentId"].(string)
		deployment, ok := s.deployments[id]
		if !ok {
			graphQLRefusal(w, "NOT_FOUND", "site deployment not found")
			return
		}
		missing, mismatched := []string{}, []string{}
		for _, path := range slices.Sorted(maps.Keys(deployment.sha256)) {
			object, stored := s.objects[id+"/"+path]
			sum := sha256.Sum256(object.body)
			switch {
			case !stored:
				missing = append(missing, path)
			case hex.EncodeToString(sum[:]) != deployment.sha256[path]:
				mismatched = append(mismatched, path)
			}
		}
		if len(missing)+len(mismatched) > 0 {
			refuseSite(w, "SITE_UPLOAD_INCOMPLETE", map[string]any{"missing": missing, "mismatched": mismatched})
			return
		}
		s.live[deployment.site] = id
		if s.disableOnPublish {
			s.disabled[deployment.site] = true
		}
		if s.afterPublish != nil {
			s.afterPublish()
		}
		if s.routed(w, f, client, app) {
			writeData(w, map[string]any{"publishSiteDeployment": s.view(deployment.site)})
		}
	case "RemoveEnvironmentSite":
		env, _ := variables["environmentId"].(string)
		if !f.hasEnvironment(app, env) {
			graphQLRefusal(w, "NOT_FOUND", "environment not found")
			return
		}
		if !s.removeSite("env:" + env) {
			graphQLRefusal(w, "NOT_FOUND", "site not found")
			return
		}
		if s.routed(w, f, client, app) {
			writeData(w, map[string]any{"removeEnvironmentSite": map[string]any{"id": env}})
		}
	case "DeleteApplicationSite":
		if variables["applicationId"] != app {
			graphQLRefusal(w, "NOT_FOUND", "application not found")
			return
		}
		if !s.removeSite("app:" + app) {
			graphQLRefusal(w, "NOT_FOUND", "site not found")
			return
		}
		if s.routed(w, f, client, app) {
			writeData(w, map[string]any{"deleteApplicationSite": map[string]any{"id": app}})
		}
	}
}

// refuseSite answers code with the server's message for it, and extensions beside the code.
func refuseSite(w http.ResponseWriter, code string, extensions map[string]any) {
	graphQLRefusalWith(w, code, siteRefusalMessages[code], extensions)
}

// signedSiteHeaders are the headers the server signs a site file's upload with (sign-site-upload.ts in the multipaas
// repo): its content type and cache policy from its name, its SHA-256, and SSE-S3.
func signedSiteHeaders(file, digest string) map[string]string {
	sum, _ := hex.DecodeString(digest)
	contentType, ok := fakeContentTypes[filepath.Ext(file)]
	if !ok {
		contentType = "application/octet-stream"
	}
	cacheControl := "public, max-age=300"
	if ext := filepath.Ext(file); ext == ".html" || ext == ".htm" {
		cacheControl = "no-cache"
	}
	return map[string]string{
		"content-type":                 contentType,
		"cache-control":                cacheControl,
		"x-amz-checksum-sha256":        base64.StdEncoding.EncodeToString(sum),
		"x-amz-sdk-checksum-algorithm": "SHA256",
		"x-amz-server-side-encryption": "AES256",
	}
}

// uploadURL is a deployment's presigned PUT address for path: each path segment escaped, as S3 escapes a key in a
// presigned URL, so a "#", "?" or "%" in a file name stays part of the name.
func (s *fakeSites) uploadURL(deployment, path string) string {
	segments := strings.Split(path, "/")
	for i, segment := range segments {
		segments[i] = url.PathEscape(segment)
	}
	return s.server.URL + "/s3/" + deployment + "/" + strings.Join(segments, "/")
}

// hasEnvironment reports whether env is one of app's environments.
func (f *fakeMultiPaaS) hasEnvironment(app, env string) bool {
	return slices.ContainsFunc(f.environments[app], func(e fakeEnv) bool { return e.id == env })
}

// removeSite takes key's site away, reporting false when there never was one there.
func (s *fakeSites) removeSite(key string) bool {
	if s.siteIDs[key] == "" {
		return s.removed[key]
	}
	delete(s.siteIDs, key)
	s.removed[key] = true
	return true
}

// routed writes the application's routes and reports true, or, while routesPending lasts, answers SITE_ROUTES_PENDING
// without writing them and reports false.
func (s *fakeSites) routed(w http.ResponseWriter, f *fakeMultiPaaS, client, app string) bool {
	if s.routesPending > 0 {
		s.routesPending--
		refuseSite(w, "SITE_ROUTES_PENDING", nil)
		return false
	}
	s.route(f, client, app)
	return true
}

// served is the site an environment's URL serves: its own once that is live, else the application's once that is.
// A disabled site serves nothing, and an environment whose own site is disabled doesn't fall back to the application's.
func (s *fakeSites) served(app, env string) string {
	for _, site := range []string{s.siteIDs["env:"+env], s.siteIDs["app:"+app]} {
		if s.disabled[site] {
			return ""
		}
		if site != "" && s.live[site] != "" {
			return site
		}
	}
	return ""
}

// route points each of the application's environment labels at the deployment it now serves. A label whose route
// changes keeps answering its previous deployment for propagation more GETs.
func (s *fakeSites) route(f *fakeMultiPaaS, client, app string) {
	for _, e := range f.environments[app] {
		label, want := f.siteLabel(client, app, e), s.live[s.served(app, e.id)]
		if s.routes[label] != want {
			s.previous[label], s.stale[label], s.routes[label] = s.routes[label], s.propagation, want
		}
	}
}

// view is a site as the API shows it, or nil for "".
func (s *fakeSites) view(site string) any {
	if site == "" {
		return nil
	}
	var active any
	if id := s.live[site]; id != "" {
		d := s.deployments[id]
		active = map[string]any{"id": id, "status": "live", "fileCount": len(d.sha256), "totalBytes": d.bytes, "publishedAt": "2026-11-24T12:00:00.000Z"}
	}
	return map[string]any{"id": site, "disabled": s.disabled[site], "activeDeployment": active}
}

// receive is a presigned PUT.
func (s *fakeSites) receive(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	key := r.PathValue("deployment") + "/" + r.PathValue("path")
	path := r.PathValue("path")
	s.mu.Lock()
	s.attempts[path]++
	s.inFlight++
	s.peak = max(s.peak, s.inFlight)
	delay := s.putDelay
	s.mu.Unlock()
	time.Sleep(delay)
	s.mu.Lock()
	defer s.mu.Unlock()
	s.inFlight--
	if s.failPuts[path] > 0 {
		s.failPuts[path]--
		http.Error(w, cmp.Or(s.failBody, "SlowDown"), cmp.Or(s.failStatus, http.StatusServiceUnavailable))
		return
	}
	if r.ContentLength != s.sizes[key] {
		http.Error(w, "SignatureDoesNotMatch", http.StatusForbidden)
		return
	}
	for name, value := range s.signed[key] {
		if r.Header.Get(name) != value {
			http.Error(w, "SignatureDoesNotMatch", http.StatusForbidden)
			return
		}
	}
	sum := sha256.Sum256(body)
	if r.Header.Get("x-amz-checksum-sha256") != base64.StdEncoding.EncodeToString(sum[:]) {
		http.Error(w, "BadDigest", http.StatusBadRequest)
		return
	}
	if path != s.dropPut {
		s.objects[key] = fakeUpload{body: body, header: r.Header.Clone(), contentLength: r.ContentLength}
	}
	w.WriteHeader(http.StatusOK)
}

// serve is the sites edge answering a label's /.multipaas/deployment.
func (s *fakeSites) serve(w http.ResponseWriter, r *http.Request) {
	label := r.PathValue("label")
	s.mu.Lock()
	defer s.mu.Unlock()
	s.polls++
	served := s.routes[label]
	if s.stale[label] > 0 {
		s.stale[label]--
		served = s.previous[label]
	}
	if served == "" {
		http.Error(w, "No site here", http.StatusNotFound)
		return
	}
	if s.pollStatus != 0 {
		w.WriteHeader(s.pollStatus)
	}
	_, _ = io.WriteString(w, served)
}

// siteLabel is the hostname label of the site URL the fake gives an environment.
func (f *fakeMultiPaaS) siteLabel(client, app string, e fakeEnv) string {
	return strings.TrimSuffix(strings.TrimPrefix(f.siteURL(client, app, e), "https://"), ".example.test")
}
