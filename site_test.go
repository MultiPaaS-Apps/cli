package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"maps"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"
)

const siteApp = "app-a-000"

func TestSiteDeployUploadsEveryFileWithItsSignedHeadersThenWaitsUntilTheSiteServesIt(t *testing.T) {
	fake := siteFake(t)
	fake.sites.propagation = 2
	dir := t.TempDir()
	index, script := []byte("<!doctype html><title>Shop</title>"), []byte("console.log('shop')")
	writeNested(t, dir, "index.html", index)
	writeNested(t, dir, "assets/v2/app.js", script)
	if err := os.Symlink(filepath.Join(dir, "assets", "v2"), filepath.Join(dir, "assets", "latest")); err != nil {
		t.Fatal(err)
	}

	got := fake.run(t, "site", "deploy", dir, "--app", siteApp, "--json")

	if got.exit != exitOK {
		t.Fatalf("exit %d: %s %s", got.exit, got.stdout, got.stderr)
	}
	siteURL := fake.sites.server.URL + "/app-a-000--acme-co"
	if compact(got.stdout) != fmt.Sprintf(`{"deploymentId":"dep-2","siteUrl":%q,"fileCount":3,"totalBytes":%d}`, siteURL, len(index)+2*len(script)) {
		t.Fatalf("stdout %s", got.stdout)
	}
	sent := fake.mutations[0]
	if _, named := sent["environmentId"]; named || sent["applicationId"] != siteApp || sent["spaFallback"] != true {
		t.Fatalf("sent %v", sent)
	}
	indexSum, scriptSum := sha256.Sum256(index), sha256.Sum256(script)
	listed := map[string][2]any{}
	for _, item := range sent["files"].([]any) {
		file := item.(map[string]any)
		if keys := slices.Sorted(maps.Keys(file)); !reflect.DeepEqual(keys, []string{"path", "sha256", "sizeBytes"}) {
			t.Fatalf("file sent with %v", keys)
		}
		listed[file["path"].(string)] = [2]any{file["sizeBytes"], file["sha256"]}
	}
	want := map[string][2]any{
		"index.html":           {float64(len(index)), hex.EncodeToString(indexSum[:])},
		"assets/latest/app.js": {float64(len(script)), hex.EncodeToString(scriptSum[:])},
		"assets/v2/app.js":     {float64(len(script)), hex.EncodeToString(scriptSum[:])},
	}
	if !reflect.DeepEqual(listed, want) {
		t.Fatalf("files %v, want %v", listed, want)
	}
	for path, body := range map[string][]byte{"index.html": index, "assets/latest/app.js": script, "assets/v2/app.js": script} {
		received, ok := fake.sites.objects["dep-2/"+path]
		if !ok || string(received.body) != string(body) || received.contentLength != int64(len(body)) {
			t.Fatalf("%s: upload %+v", path, received)
		}
		signed := fake.sites.signed["dep-2/"+path]
		if len(signed) == 0 {
			t.Fatalf("%s: no signed headers", path)
		}
		for name, value := range signed {
			if received.header.Get(name) != value {
				t.Fatalf("%s: header %s = %q, want %q", path, name, received.header.Get(name), value)
			}
		}
		if received.header.Get("Authorization") != "" || received.header.Get("X-Multipaas-Scope") != "" {
			t.Fatalf("%s: headers %v", path, received.header)
		}
	}
	if fake.sites.polls != 3 || !strings.Contains(got.stderr, "waiting for "+siteURL) {
		t.Fatalf("polls %d, stderr %q", fake.sites.polls, got.stderr)
	}
}

func TestSiteDeployRetriesAPutThatFailsWithA5xxUpToThreeTimes(t *testing.T) {
	fake := siteFake(t)
	dir := t.TempDir()
	writeNested(t, dir, "index.html", []byte("<p>hi</p>"))
	fake.sites.failPuts["index.html"] = 3

	recovered := fake.run(t, "site", "deploy", dir, "--app", siteApp, "--json")
	fake.sites.failPuts["index.html"] = 4
	refused := fake.run(t, "site", "deploy", dir, "--app", siteApp, "--json")

	if recovered.exit != exitOK {
		t.Fatalf("exit %d: %s", recovered.exit, recovered.stdout)
	}
	assertFailure(t, refused, exitRefused, "UPLOAD_REFUSED")
	if !strings.Contains(refused.stdout, "upload of index.html refused (HTTP 503)") || fake.sites.publishCount() != 1 {
		t.Fatalf("publishes %d: %s", fake.sites.publishCount(), refused.stdout)
	}
}

func TestSiteDeployGivesUpAtOnceOnAPutThatFailsWithA4xx(t *testing.T) {
	fake := siteFake(t)
	dir := t.TempDir()
	writeNested(t, dir, "index.html", []byte("<p>hi</p>"))
	fake.sites.failStatus = 403
	fake.sites.failPuts["index.html"] = 4

	got := fake.run(t, "site", "deploy", dir, "--app", siteApp, "--json")

	assertFailure(t, got, exitRefused, "UPLOAD_REFUSED")
	if !strings.Contains(got.stdout, "upload of index.html refused (HTTP 403)") || fake.sites.attempts["index.html"] != 1 {
		t.Fatalf("PUT %d times: %s", fake.sites.attempts["index.html"], got.stdout)
	}
}

func TestSiteDeployUploadsAtMostEightFilesAtOnce(t *testing.T) {
	fake := siteFake(t)
	fake.sites.putDelay = 20 * time.Millisecond
	dir := t.TempDir()
	for i := range 24 {
		writeNested(t, dir, fmt.Sprintf("page-%02d.html", i), []byte(fmt.Sprintf("<p>%d</p>", i)))
	}

	got := fake.run(t, "site", "deploy", dir, "--app", siteApp, "--json")

	if got.exit != exitOK {
		t.Fatalf("exit %d: %s", got.exit, got.stdout)
	}
	if fake.sites.peak < 2 || fake.sites.peak > 8 {
		t.Fatalf("at most %d PUTs at once, want 2 to 8", fake.sites.peak)
	}
}

func TestSiteDeployWithEnvPublishesThatEnvironmentsOwnSiteWithoutSPA(t *testing.T) {
	fake := siteFake(t)
	fake.run(t, "envs", "create", "--app", siteApp, "--name", "staging", "--json")
	dir := t.TempDir()
	writeNested(t, dir, "index.html", []byte("<p>staging</p>"))

	got := fake.run(t, "site", "deploy", dir, "--app", siteApp, "--env", "env-new-2", "--no-spa", "--json")

	if got.exit != exitOK {
		t.Fatalf("exit %d: %s", got.exit, got.stdout)
	}
	if sent := fake.mutations[len(fake.mutations)-2]; sent["environmentId"] != "env-new-2" || sent["spaFallback"] != false {
		t.Fatalf("sent %v", sent)
	}
	stagingURL := fake.sites.server.URL + "/staging--app-a-000--acme-co"
	status := fake.run(t, "site", "status", "--app", siteApp, "--env", "env-new-2", "--json")
	if compact(status.stdout) != fmt.Sprintf(`{"environmentId":"env-new-2","siteUrl":%q,"source":"environment","site":{"id":"site-1","disabled":false,"activeDeployment":{"id":"dep-2","status":"live","fileCount":1,"totalBytes":14,"publishedAt":"2026-11-24T12:00:00.000Z"}},"disabled":false}`, stagingURL) {
		t.Fatalf("status %s", status.stdout)
	}
	unpublished := fake.run(t, "site", "status", "--app", siteApp, "--json")
	if compact(unpublished.stdout) != fmt.Sprintf(`{"environmentId":"env-default","siteUrl":%q,"source":"none","site":null,"disabled":false}`, fake.sites.server.URL+"/app-a-000--acme-co") {
		t.Fatalf("default status %s", unpublished.stdout)
	}
}

func TestSiteDeployAsksAgainWhileRoutesArePending(t *testing.T) {
	fake := siteFake(t)
	dir := t.TempDir()
	writeNested(t, dir, "index.html", []byte("<p>hi</p>"))
	fake.sites.routesPending = 3

	published := fake.run(t, "site", "deploy", dir, "--app", siteApp)
	publishes := fake.sites.publishCount()
	fake.sites.routesPending = 4
	pending := fake.run(t, "site", "deploy", dir, "--app", siteApp)

	if published.exit != exitOK || !strings.Contains(published.stdout, "deployment dep-2 is live at "+fake.sites.server.URL+"/app-a-000--acme-co") || publishes != 4 {
		t.Fatalf("exit %d, publishes %d: %s %s", published.exit, publishes, published.stdout, published.stderr)
	}
	if pending.exit != exitRefused || !strings.Contains(pending.stderr, "(SITE_ROUTES_PENDING)") || fake.sites.publishCount() != 8 {
		t.Fatalf("exit %d, publishes %d: %s", pending.exit, fake.sites.publishCount(), pending.stderr)
	}
}

func TestSiteDeployKeepsTheDeploymentIDWhenEveryPublishAnswersRoutesPending(t *testing.T) {
	fake := siteFake(t)
	dir := t.TempDir()
	writeNested(t, dir, "index.html", []byte("<p>hi</p>"))
	fake.sites.routesPending = 4

	got := fake.run(t, "site", "deploy", dir, "--app", siteApp)

	if got.exit != exitRefused || !strings.Contains(got.stderr, "deployment dep-2 is published, but its routes aren't written yet: ") ||
		!strings.Contains(got.stderr, "(SITE_ROUTES_PENDING)") || !strings.Contains(got.stderr, "The deployment is published, but the site URL doesn't serve it yet.") {
		t.Fatalf("exit %d, stderr %s", got.exit, got.stderr)
	}
}

func TestSiteDeployNamesThePublishedDeploymentWhenRetriesAfterRoutesPendingFailDifferently(t *testing.T) {
	fake := siteFake(t)
	dir := t.TempDir()
	writeNested(t, dir, "index.html", []byte("<p>hi</p>"))
	fake.sites.routesPending = 1
	fake.sites.afterPublish = func() { fake.sites.failPublishes = []int{503, 503, 503} }

	got := fake.run(t, "site", "deploy", dir, "--app", siteApp)

	if got.exit != exitRefused || !strings.Contains(got.stderr, "deployment dep-2 is published, but writing its routes didn't finish: ") ||
		strings.Contains(got.stderr, "aren't written yet") || fake.sites.publishCount() != 4 {
		t.Fatalf("exit %d, publishes %d: %s", got.exit, fake.sites.publishCount(), got.stderr)
	}
}

func TestUploadSiteReportsACancelledContextInsteadOfSuccess(t *testing.T) {
	fake := siteFake(t)
	dir := t.TempDir()
	local := writeNested(t, dir, "index.html", []byte("<p>hi</p>"))
	files := []siteFile{{path: "index.html", local: local, size: 9}}
	uploads := []SiteUploadParts{{Path: "index.html", Url: fake.sites.uploadURL("dep-2", "index.html")}}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err := uploadSite(ctx, files, uploads)

	if !errors.Is(err, context.Canceled) || fake.sites.attempts["index.html"] != 0 {
		t.Fatalf("err %v, PUTs %d", err, fake.sites.attempts["index.html"])
	}
}

func TestSiteDeployPublishesTheDirectoryTheKernelResolvesForADotDotAfterALink(t *testing.T) {
	fake := siteFake(t)
	parent, elsewhere := t.TempDir(), t.TempDir()
	writeNested(t, parent, "dist/index.html", []byte("<p>no</p>x"))
	writeNested(t, elsewhere, "dist/index.html", []byte("<p>hi</p>"))
	writeNested(t, elsewhere, "dist/app.js", []byte("1"))
	writeNested(t, elsewhere, "sub/keep", []byte("k"))
	if err := os.Symlink(filepath.Join(elsewhere, "sub"), filepath.Join(parent, "link")); err != nil {
		t.Fatal(err)
	}

	got := fake.run(t, "site", "deploy", parent+"/link/../dist", "--app", siteApp, "--json")

	if got.exit != exitOK || !strings.Contains(got.stdout, `"fileCount": 2`) || !strings.Contains(got.stdout, `"totalBytes": 10`) {
		t.Fatalf("exit %d: %s %s", got.exit, got.stdout, got.stderr)
	}

	t.Chdir(filepath.Join(parent, "link"))
	fromInside := fake.run(t, "site", "deploy", "../dist", "--app", siteApp, "--json")

	if fromInside.exit != exitOK || !strings.Contains(fromInside.stdout, `"fileCount": 2`) || !strings.Contains(fromInside.stdout, `"totalBytes": 10`) {
		t.Fatalf("from inside the link: exit %d: %s %s", fromInside.exit, fromInside.stdout, fromInside.stderr)
	}
}

func TestSiteDeployFollowsAnAbsoluteLinkInsideARelativeDirectory(t *testing.T) {
	fake := siteFake(t)
	parent := t.TempDir()
	writeNested(t, parent, "site/index.html", []byte("<p>hi</p>"))
	writeNested(t, parent, "site/real/app.js", []byte("1"))
	real, err := filepath.EvalSymlinks(filepath.Join(parent, "site", "real"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(real, filepath.Join(parent, "site", "link")); err != nil {
		t.Fatal(err)
	}
	t.Chdir(parent)

	got := fake.run(t, "site", "deploy", "site", "--app", siteApp, "--json")

	if got.exit != exitOK || !strings.Contains(got.stdout, `"fileCount": 3`) {
		t.Fatalf("exit %d: %s %s", got.exit, got.stdout, got.stderr)
	}
}

func TestSiteDeployFailsWhenNoEnvironmentServesAPublishedSiteBecauseItWasDisabled(t *testing.T) {
	fake := siteFake(t)
	dir := t.TempDir()
	writeNested(t, dir, "index.html", []byte("<p>hi</p>"))
	fake.sites.disableOnPublish = true

	got := fake.run(t, "site", "deploy", dir, "--app", siteApp, "--json")

	assertFailure(t, got, exitRefused, "SITE_DISABLED")
	if !strings.Contains(got.stdout, "deployment dep-2 is published, but it could not be verified live: MultiPaaS has disabled a site of this application") {
		t.Fatalf("stdout %s", got.stdout)
	}
}

func TestSiteDeploySucceedsWithNoURLWhenTheApplicationSiteIsDisabledButEveryEnvironmentServesItsOwn(t *testing.T) {
	fake := siteFake(t)
	dir := t.TempDir()
	writeNested(t, dir, "index.html", []byte("<p>hi</p>"))
	setup := fake.run(t, "site", "deploy", dir, "--app", siteApp, "--env", "env-default", "--json")
	if setup.exit != exitOK {
		t.Fatalf("setup deploy: exit %d: %s %s", setup.exit, setup.stdout, setup.stderr)
	}
	fake.sites.disableOnPublish = true

	got := fake.run(t, "site", "deploy", dir, "--app", siteApp)

	if got.exit != exitOK || got.stdout != "deployment dep-4 is published (1 files, 9 bytes), but every environment has a live or disabled site of its own, so none serves it\n" {
		t.Fatalf("exit %d: %s %s", got.exit, got.stdout, got.stderr)
	}
}

func TestSiteDeploySucceedsWithNoURLWhenAnEnvironmentsOwnSiteIsDisabledAfterAnApplicationSitePublish(t *testing.T) {
	fake := siteFake(t)
	dir := t.TempDir()
	writeNested(t, dir, "index.html", []byte("<p>hi</p>"))
	setup := fake.run(t, "site", "deploy", dir, "--app", siteApp, "--env", "env-default", "--json")
	if setup.exit != exitOK {
		t.Fatalf("setup deploy: exit %d: %s %s", setup.exit, setup.stdout, setup.stderr)
	}
	fake.sites.afterPublish = func() { fake.sites.disabled[fake.sites.siteIDs["env:env-default"]] = true }

	got := fake.run(t, "site", "deploy", dir, "--app", siteApp)

	if got.exit != exitOK || got.stdout != "deployment dep-4 is published (1 files, 9 bytes), but every environment has a live or disabled site of its own, so none serves it\n" {
		t.Fatalf("exit %d: %s %s", got.exit, got.stdout, got.stderr)
	}
}

func TestSiteDeployNamesTheDeploymentWhenTheApplicationSiteIsRemovedAfterItPublished(t *testing.T) {
	fake := siteFake(t)
	dir := t.TempDir()
	writeNested(t, dir, "index.html", []byte("<p>hi</p>"))
	fake.sites.afterPublish = func() { fake.sites.removeSite("app:" + siteApp) }

	got := fake.run(t, "site", "deploy", dir, "--app", siteApp, "--json")

	assertFailure(t, got, exitRefused, "CLI_ERROR")
	if !strings.Contains(got.stdout, "deployment dep-2 is published, but it could not be verified live: no environment serves the site that was published; site status --env ENV_ID shows what an environment serves") {
		t.Fatalf("stdout %s", got.stdout)
	}
}

func TestSiteDeployRefusesADirectoryOverALimitBeforeSendingAnything(t *testing.T) {
	for _, c := range []struct {
		name         string
		write        func(t *testing.T, dir string)
		limit, path  string
		most, actual int64
	}{
		{"a file over 25 MiB", func(t *testing.T, dir string) { sparse(t, dir, "big.bin", 25<<20+1) }, "fileBytes", "big.bin", 25 << 20, 25<<20 + 1},
		{"over 100 MiB in all", func(t *testing.T, dir string) {
			for i := range 5 {
				sparse(t, dir, fmt.Sprintf("part-%d.bin", i), 21<<20)
			}
		}, "totalBytes", "", 100 << 20, 5 * 21 << 20},
		{"over 2000 files", func(t *testing.T, dir string) {
			for i := range 2001 {
				writeNested(t, dir, fmt.Sprintf("f/%04d.txt", i), []byte("x"))
			}
		}, "files", "", 2000, 2001},
		{"an SPA index over 4 MiB", func(t *testing.T, dir string) { sparse(t, dir, "index.html", 4<<20+1) }, "spaIndexBytes", "index.html", 4 << 20, 4<<20 + 1},
		{"paths over 320 KiB in all", func(t *testing.T, dir string) { longPaths(t, dir, 1280, 1) }, "pathBytes", "", 320 << 10, 320<<10 + 1},
	} {
		t.Run(c.name, func(t *testing.T) {
			fake := siteFake(t)
			dir := t.TempDir()
			c.write(t, dir)

			got := fake.run(t, "site", "deploy", dir, "--app", siteApp, "--json")

			assertFailure(t, got, exitRefused, "SITE_LIMIT_EXCEEDED")
			var failure struct{ Error cliError }
			decode(t, got.stdout, &failure)
			if e := failure.Error; e.Limit != c.limit || e.Max != c.most || e.Actual != c.actual || e.Path != c.path {
				t.Fatalf("refusal %+v", e)
			}
			if len(fake.scopes) != 0 {
				t.Fatalf("sent %v", fake.operations)
			}
		})
	}
}

func TestSiteDeployPublishesPathsOfExactly320KiB(t *testing.T) {
	fake := siteFake(t)
	dir := t.TempDir()
	longPaths(t, dir, 1280, 0)

	got := fake.run(t, "site", "deploy", dir, "--app", siteApp, "--json")

	if got.exit != exitOK {
		t.Fatalf("exit %d: %s", got.exit, got.stdout)
	}
}

func TestSiteDeployWithoutSPAPublishesAnIndexOver4MiB(t *testing.T) {
	fake := siteFake(t)
	dir := t.TempDir()
	sparse(t, dir, "index.html", 4<<20+1)

	got := fake.run(t, "site", "deploy", dir, "--app", siteApp, "--no-spa", "--json")

	if got.exit != exitOK {
		t.Fatalf("exit %d: %s", got.exit, got.stdout)
	}
}

func TestSiteDeployRefusesALinkItCannotFollowOrThatLeavesTheDirectory(t *testing.T) {
	fake := siteFake(t)
	dir, elsewhere := t.TempDir(), t.TempDir()
	writeNested(t, dir, "index.html", []byte("<p>hi</p>"))
	writeNested(t, elsewhere, "secret.txt", []byte("not for the site"))
	for link, target := range map[string]string{
		"secret.txt": filepath.Join(elsewhere, "secret.txt"),
		"shared":     elsewhere,
		"loop":       dir,
		"gone.png":   filepath.Join(dir, "missing.png"),
	} {
		if err := os.Symlink(target, filepath.Join(dir, link)); err != nil {
			t.Fatal(err)
		}
	}

	got := fake.run(t, "site", "deploy", dir, "--app", siteApp, "--json")

	assertFailure(t, got, exitRefused, "SITE_DIR_INVALID")
	var failure struct{ Error cliError }
	decode(t, got.stdout, &failure)
	for _, part := range []string{
		"Cannot follow gone.png: a link that points at nothing",
		"Cannot follow loop: a link to a folder it sits inside",
		"Cannot follow secret.txt, shared: a link that leads outside the directory",
		"Nothing was uploaded.",
	} {
		if !strings.Contains(failure.Error.Message, part) {
			t.Fatalf("message %q does not contain %q", failure.Error.Message, part)
		}
	}
	if len(fake.scopes) != 0 {
		t.Fatalf("sent %v", fake.operations)
	}
}

func TestSiteDeployGivesUpWhenTheSiteDoesNotServeItInTime(t *testing.T) {
	fake := siteFake(t)
	fake.sites.propagation = 1 << 20
	siteWaitLimit = 20 * time.Millisecond
	dir := t.TempDir()
	writeNested(t, dir, "index.html", []byte("<p>hi</p>"))

	got := fake.run(t, "site", "deploy", dir, "--app", siteApp, "--json")

	assertFailure(t, got, exitRefused, "SITE_NOT_LIVE_YET")
	if !strings.Contains(got.stdout, "deployment dep-2 is published, but "+fake.sites.server.URL+"/app-a-000--acme-co did not serve it") {
		t.Fatalf("stdout %s", got.stdout)
	}
}

func TestSiteDeployWaitsForTheDeploymentAnsweredWithAnOK(t *testing.T) {
	fake := siteFake(t)
	fake.sites.pollStatus = http.StatusServiceUnavailable
	siteWaitLimit = 20 * time.Millisecond
	dir := t.TempDir()
	writeNested(t, dir, "index.html", []byte("<p>hi</p>"))

	got := fake.run(t, "site", "deploy", dir, "--app", siteApp, "--json")

	fake.sites.mu.Lock()
	probes := slices.Clone(fake.sites.probes)
	fake.sites.mu.Unlock()
	if !slices.Contains(probes, probeAnswer{status: http.StatusServiceUnavailable, body: "dep-2"}) {
		t.Fatalf("probes %v", probes)
	}
	assertFailure(t, got, exitRefused, "SITE_NOT_LIVE_YET")
}

func TestSiteDeployRefusesAnUploadTheDirectoryDoesNotHold(t *testing.T) {
	fake := siteFake(t)
	fake.sites.askFor = "extra.html"
	dir := t.TempDir()
	writeNested(t, dir, "index.html", []byte("<p>hi</p>"))

	got := fake.run(t, "site", "deploy", dir, "--app", siteApp, "--json")

	assertFailure(t, got, exitRefused, "CLI_ERROR")
	if !strings.Contains(got.stdout, "the server asked for extra.html, which is not in the directory") || len(fake.sites.objects) != 0 || fake.sites.publishCount() != 0 {
		t.Fatalf("%d uploads, %d publishes: %s", len(fake.sites.objects), fake.sites.publishCount(), got.stdout)
	}
}

func TestSiteDeployRefusesBothEnvAndAppSite(t *testing.T) {
	fake := siteFake(t)
	dir := t.TempDir()
	writeNested(t, dir, "index.html", []byte("<p>hi</p>"))

	got := fake.run(t, "site", "deploy", dir, "--app", siteApp, "--env", "env-default", "--app-site")

	if got.exit == exitOK || !strings.Contains(got.stderr, "[app-site env] were all set") || len(fake.mutations) != 0 {
		t.Fatalf("exit %d, sent %v: %s", got.exit, fake.operations, got.stderr)
	}
}

func TestSiteDeployKeepsTheDetailsOfTheServersRefusal(t *testing.T) {
	fake := siteFake(t)
	dir := t.TempDir()
	writeNested(t, dir, "index.html", []byte("<p>hi</p>"))

	fake.sites.refuse["CreateSiteDeployment"] = map[string]any{"code": "SITE_LIMIT_EXCEEDED", "limit": "fileBytes", "max": 26214400, "actual": 26214401, "path": "index.html"}
	limit := fake.run(t, "site", "deploy", dir, "--app", siteApp, "--json")
	delete(fake.sites.refuse, "CreateSiteDeployment")
	fake.sites.refuse["PublishSiteDeployment"] = map[string]any{"code": "SITE_UPLOAD_INCOMPLETE", "missing": []string{"a.js"}, "mismatched": []string{"index.html"}}
	incomplete := fake.run(t, "site", "deploy", dir, "--app", siteApp, "--json")

	var limited, unfinished struct{ Error cliError }
	decode(t, limit.stdout, &limited)
	decode(t, incomplete.stdout, &unfinished)
	if e := limited.Error; e.Code != "SITE_LIMIT_EXCEEDED" || e.Limit != "fileBytes" || e.Max != 26214400 || e.Actual != 26214401 || e.Path != "index.html" {
		t.Fatalf("refusal %+v", e)
	}
	if e := unfinished.Error; e.Code != "SITE_UPLOAD_INCOMPLETE" || !reflect.DeepEqual(e.Missing, []string{"a.js"}) || !reflect.DeepEqual(e.Mismatched, []string{"index.html"}) {
		t.Fatalf("refusal %+v", e)
	}
}

func TestSiteRefusalsGiveAHintThatFitsTheCommand(t *testing.T) {
	deploy := func(dir string) []string { return []string{"site", "deploy", dir, "--app", siteApp} }
	removeOwn := func(string) []string { return []string{"site", "remove", "--app", siteApp, "--env", "env-default"} }
	deleteApp := func(_ *testing.T, fake *fakeMultiPaaS, _ string) { fake.sites.deletedApps[siteApp] = true }
	refuse := func(operation string, refusal map[string]any) func(*testing.T, *fakeMultiPaaS, string) {
		return func(_ *testing.T, fake *fakeMultiPaaS, _ string) { fake.sites.refuse[operation] = refusal }
	}
	for _, c := range []struct {
		name  string
		setup func(t *testing.T, fake *fakeMultiPaaS, dir string)
		args  func(dir string) []string
		code  string
		hint  string
	}{
		{"a limit", refuse("CreateSiteDeployment", map[string]any{"code": "SITE_LIMIT_EXCEEDED", "limit": "totalBytes", "max": 104857600, "actual": 104857601}), deploy,
			"SITE_LIMIT_EXCEEDED", "A deployment holds at most 2000 files and 100 MiB, and a file at most 25 MiB."},
		{"the path limit", refuse("CreateSiteDeployment", map[string]any{"code": "SITE_LIMIT_EXCEEDED", "limit": "pathBytes", "max": 327680, "actual": 327681}), deploy,
			"SITE_LIMIT_EXCEEDED", "A deployment's file paths may total at most 320 KiB"},
		{"the SPA index limit", refuse("CreateSiteDeployment", map[string]any{"code": "SITE_LIMIT_EXCEEDED", "limit": "spaIndexBytes", "max": 4194304, "actual": 4194305, "path": "index.html"}), deploy,
			"SITE_LIMIT_EXCEEDED", "Make it smaller, or run site deploy again with --no-spa."},
		{"a path", refuse("CreateSiteDeployment", map[string]any{"code": "SITE_PATH_INVALID", "path": "index.html"}), deploy,
			"SITE_PATH_INVALID", `A site can't hold "index.html": .multipaas/ is reserved`},
		{"a deleted application on deploy", deleteApp, deploy,
			"UNAUTHORIZED", "your access to the application was removed, or it was deleted, while the command ran"},
		{"a deleted application on status", deleteApp, func(string) []string { return []string{"site", "status", "--app", siteApp} },
			"UNAUTHORIZED", "Your access to the application was removed, or it was deleted, while the command ran. Check --app against apps list."},
		{"a missing upload", func(_ *testing.T, fake *fakeMultiPaaS, _ string) { fake.sites.dropPut = "index.html" }, deploy,
			"SITE_UPLOAD_INCOMPLETE", "Not uploaded: index.html.\nNothing was published. Run site deploy again once the directory stops changing."},
		{"routes pending on deploy", func(_ *testing.T, fake *fakeMultiPaaS, _ string) { fake.sites.routesPending = 4 }, deploy,
			"SITE_ROUTES_PENDING", "The deployment is published, but the site URL doesn't serve it yet. Run site deploy again"},
		{"routes pending on remove", func(t *testing.T, fake *fakeMultiPaaS, dir string) {
			fake.run(t, "site", "deploy", dir, "--app", siteApp, "--env", "env-default", "--json")
			fake.sites.routesPending = 1
		}, removeOwn, "SITE_ROUTES_PENDING", "The site is removed, but the site URL doesn't show it yet. Run the same site remove again"},
		{"a disabled site on deploy", refuse("CreateSiteDeployment", map[string]any{"code": "SITE_DISABLED", "siteId": "site-9"}), deploy,
			"SITE_DISABLED", "none of its sites can be published or removed"},
		{"a disabled site on remove", refuse("RemoveEnvironmentSite", map[string]any{"code": "SITE_DISABLED", "siteId": "site-9"}), removeOwn,
			"SITE_DISABLED", "none of its sites can be published or removed"},
		{"a file changed while uploading", refuse("PublishSiteDeployment", map[string]any{"code": "SITE_UPLOAD_INCOMPLETE", "missing": []string{}, "mismatched": []string{"index.html"}}), deploy,
			"SITE_UPLOAD_INCOMPLETE", "Changed after site deploy read them: index.html."},
		{"an unpublishable deployment", refuse("PublishSiteDeployment", map[string]any{"code": "SITE_DEPLOYMENT_NOT_PUBLISHABLE", "status": "superseded"}), deploy,
			"SITE_DEPLOYMENT_NOT_PUBLISHABLE", "Run site deploy again, which makes a new one."},
		{"a site not live in time", func(_ *testing.T, fake *fakeMultiPaaS, _ string) {
			fake.sites.propagation = 1 << 20
			siteWaitLimit = 20 * time.Millisecond
		}, deploy,
			"SITE_NOT_LIVE_YET", "Run site status in a few minutes"},
		{"an unknown environment on deploy", nil, func(dir string) []string {
			return []string{"site", "deploy", dir, "--app", siteApp, "--env", "env-gone"}
		},
			"NOT_FOUND", "Check --app against apps list, and --env against envs list --app APP_ID."},
		{"an unknown environment on status", nil, func(string) []string { return []string{"site", "status", "--app", siteApp, "--env", "env-gone"} },
			"NOT_FOUND", "Check --app against apps list, and --env against envs list --app APP_ID."},
		{"no site to remove", nil, removeOwn,
			"NOT_FOUND", "There is no site to remove there, the environment isn't one of the application's or is being removed, or the application isn't one you can reach."},
		{"no permission to publish", refuse("CreateSiteDeployment", map[string]any{"code": "UNAUTHORIZED"}), deploy,
			"UNAUTHORIZED", "Publishing or removing a site needs environment.manage"},
		{"access removed after the scope resolved", refuse("Sites", map[string]any{"code": "UNAUTHORIZED"}), func(string) []string { return []string{"site", "status", "--app", siteApp} },
			"UNAUTHORIZED", "Your access to the application was removed, or it was deleted, while the command ran."},
		{"a server failure on deploy", refuse("CreateSiteDeployment", map[string]any{"code": "INTERNAL_SERVER_ERROR", "unexpected": true}), deploy,
			"INTERNAL_SERVER_ERROR", "Something failed on the server. Run site deploy again; publishing is safe to repeat."},
		{"a server failure on status", refuse("Sites", map[string]any{"code": "INTERNAL_SERVER_ERROR", "unexpected": true}), func(string) []string { return []string{"site", "status", "--app", siteApp} },
			"INTERNAL_SERVER_ERROR", "Something failed on the server. Run site status again."},
		{"a server failure on remove", refuse("RemoveEnvironmentSite", map[string]any{"code": "INTERNAL_SERVER_ERROR", "unexpected": true}), removeOwn,
			"INTERNAL_SERVER_ERROR", "Something failed on the server. Run site remove again; removing a site that is already gone succeeds."},
		{"no confirmation", nil, func(string) []string { return []string{"site", "remove", "--app", siteApp} },
			"CONFIRMATION_REQUIRED", "Run the command again with --yes"},
	} {
		t.Run(c.name, func(t *testing.T) {
			fake := siteFake(t)
			dir := t.TempDir()
			writeNested(t, dir, "index.html", []byte("<p>hi</p>"))
			if c.setup != nil {
				c.setup(t, fake, dir)
			}

			got := fake.run(t, c.args(dir)...)

			if got.exit != exitRefused || !strings.Contains(got.stderr, "("+c.code+")\n") || !strings.Contains(got.stderr, c.hint) {
				t.Fatalf("exit %d, stderr %q, want (%s) and %q", got.exit, got.stderr, c.code, c.hint)
			}
		})
	}
}

func TestRoutesPendingGivesAHintThatFitsTheCommand(t *testing.T) {
	for _, c := range []struct {
		operation string
		args      []string
		hint      string
	}{
		{"RenameApplicationUrlName", []string{"url-name", "rename", "--app", siteApp, "--name", "shop"},
			"Run the same url-name rename again, asking for the same name, to finish it"},
		{"DeleteEnvironment", []string{"envs", "delete", "--app", siteApp, "--env", "env-default"},
			"Run the same envs delete again. A NOT_FOUND then means the environment is gone; its site URL stops serving at the application's next site change or the daily reconcile."},
		{"RemoveEnvironment", []string{"envs", "remove", "--app", siteApp, "--env", "env-default"},
			"Run the same envs remove again. A NOT_FOUND then means the environment is gone"},
		{"CreateEnvironment", []string{"envs", "create", "--app", siteApp, "--name", "qa"},
			"Don't create it again: that is refused as a duplicate name."},
	} {
		t.Run(c.operation, func(t *testing.T) {
			fake := siteFake(t)
			fake.refuseOperation = map[string]string{c.operation: "SITE_ROUTES_PENDING"}

			got := fake.run(t, c.args...)

			if got.exit != exitRefused || !strings.Contains(got.stderr, "(SITE_ROUTES_PENDING)\n") || !strings.Contains(got.stderr, c.hint) {
				t.Fatalf("exit %d, stderr %q, want %q", got.exit, got.stderr, c.hint)
			}
		})
	}
}

func TestSiteDeployUploadsAnEmptyFileWithAContentLengthOfZero(t *testing.T) {
	fake := siteFake(t)
	dir := t.TempDir()
	writeNested(t, dir, "index.html", []byte("<p>hi</p>"))
	writeNested(t, dir, "empty.txt", nil)

	got := fake.run(t, "site", "deploy", dir, "--app", siteApp, "--json")

	if got.exit != exitOK {
		t.Fatalf("exit %d: %s", got.exit, got.stdout)
	}
	if received, ok := fake.sites.objects["dep-2/empty.txt"]; !ok || received.contentLength != 0 {
		t.Fatalf("upload %+v", received)
	}
}

func TestSiteDeployUploadsAFileWhoseNameHoldsAHashAQuestionMarkAndAPercent(t *testing.T) {
	fake := siteFake(t)
	dir := t.TempDir()
	writeNested(t, dir, "index.html", []byte("<p>hi</p>"))
	writeNested(t, dir, "docs/a#b?c%d e.html", []byte("<p>odd</p>"))

	got := fake.run(t, "site", "deploy", dir, "--app", siteApp, "--json")

	if got.exit != exitOK {
		t.Fatalf("exit %d: %s", got.exit, got.stdout)
	}
	if received, ok := fake.sites.objects["dep-2/docs/a#b?c%d e.html"]; !ok || string(received.body) != "<p>odd</p>" {
		t.Fatalf("uploads %v", slices.Sorted(maps.Keys(fake.sites.objects)))
	}
}

func TestSiteDeploySaysItPublishedWhenTheSiteLookupAfterwardsFails(t *testing.T) {
	fake := siteFake(t)
	dir := t.TempDir()
	writeNested(t, dir, "index.html", []byte("<p>hi</p>"))
	fake.sites.refuse["Sites"] = map[string]any{"code": "INTERNAL_SERVER_ERROR", "unexpected": true}

	got := fake.run(t, "site", "deploy", dir, "--app", siteApp, "--json")

	assertFailure(t, got, exitRefused, "INTERNAL_SERVER_ERROR")
	if !strings.Contains(got.stdout, "deployment dep-2 is published, but it could not be verified live: Unexpected error.") || fake.sites.publishCount() != 1 {
		t.Fatalf("publishes %d: %s", fake.sites.publishCount(), got.stdout)
	}
}

func TestSiteDeployReportsAFileItCannotReadNotNoResponse(t *testing.T) {
	fake := siteFake(t)
	dir := t.TempDir()
	index := writeNested(t, dir, "index.html", []byte("<p>hi</p>"))
	fake.sites.afterCreate = func() { _ = os.Remove(index) }

	got := fake.run(t, "site", "deploy", dir, "--app", siteApp, "--json")

	assertFailure(t, got, exitRefused, "CLI_ERROR")
	if !strings.Contains(got.stdout, "upload of index.html failed: the file can't be read (open ") || strings.Contains(got.stdout, "no response") ||
		strings.Contains(got.stdout, fake.sites.server.URL) || fake.sites.attempts["index.html"] != 0 || fake.sites.publishCount() != 0 {
		t.Fatalf("PUT %d times, publishes %d: %s", fake.sites.attempts["index.html"], fake.sites.publishCount(), got.stdout)
	}
}

func TestSiteDeployWithEnvFailsWhenThatEnvironmentDoesNotServeThePublishedSite(t *testing.T) {
	fake := siteFake(t)
	fake.run(t, "envs", "create", "--app", siteApp, "--name", "staging", "--json")
	dir := t.TempDir()
	writeNested(t, dir, "index.html", []byte("<p>hi</p>"))
	fake.sites.disableOnPublish = true
	siteWaitLimit = 20 * time.Millisecond

	got := fake.run(t, "site", "deploy", dir, "--app", siteApp, "--env", "env-new-2", "--json")

	assertFailure(t, got, exitRefused, "CLI_ERROR")
	if !strings.Contains(got.stdout, "deployment dep-2 is published, but it could not be verified live: environment env-new-2 doesn't serve the site that was published; site status --env env-new-2 shows what it serves") {
		t.Fatalf("stdout %s", got.stdout)
	}
}

func TestSiteDeployTellsItPublishedWhenTheApplicationIsDeletedBeforeTheLookup(t *testing.T) {
	fake := siteFake(t)
	dir := t.TempDir()
	writeNested(t, dir, "index.html", []byte("<p>hi</p>"))
	fake.sites.afterPublish = func() { fake.sites.deletedApps[siteApp] = true }

	got := fake.run(t, "site", "deploy", dir, "--app", siteApp)

	if got.exit != exitRefused || !strings.Contains(got.stderr, "deployment dep-2 is published, but it could not be verified live: unauthorized (UNAUTHORIZED)") ||
		!strings.Contains(got.stderr, "Publishing or removing a site needs environment.manage") {
		t.Fatalf("exit %d: %s", got.exit, got.stderr)
	}
}

func TestSiteDeployWithEnvFailsWhenThatEnvironmentIsGoneAfterPublish(t *testing.T) {
	fake := siteFake(t)
	fake.run(t, "envs", "create", "--app", siteApp, "--name", "staging", "--json")
	dir := t.TempDir()
	writeNested(t, dir, "index.html", []byte("<p>hi</p>"))
	fake.sites.afterPublish = func() {
		fake.environments[siteApp] = slices.DeleteFunc(fake.environments[siteApp], func(e fakeEnv) bool { return e.id == "env-new-2" })
	}

	got := fake.run(t, "site", "deploy", dir, "--app", siteApp, "--env", "env-new-2", "--json")

	assertFailure(t, got, exitRefused, "NOT_FOUND")
	if !strings.Contains(got.stdout, "deployment dep-2 is published, but it could not be verified live: environment env-new-2 is not an environment of application app-a-000") || fake.sites.polls != 0 {
		t.Fatalf("polls %d: %s", fake.sites.polls, got.stdout)
	}
}

func TestSiteDeployWaitsForTheNewDeploymentNotTheOneTheURLStillServes(t *testing.T) {
	fake := siteFake(t)
	dir := t.TempDir()
	writeNested(t, dir, "index.html", []byte("<p>first</p>"))
	first := fake.run(t, "site", "deploy", dir, "--app", siteApp, "--json")
	writeNested(t, dir, "index.html", []byte("<p>second</p>"))
	fake.sites.propagation = 3
	polls := fake.sites.polls

	second := fake.run(t, "site", "deploy", dir, "--app", siteApp, "--json")

	if first.exit != exitOK || second.exit != exitOK || !strings.Contains(second.stdout, `"deploymentId": "dep-3"`) {
		t.Fatalf("exit %d then %d: %s", first.exit, second.exit, second.stdout)
	}
	if fake.sites.polls-polls != 4 {
		t.Fatalf("polled %d times while the URL served dep-2 for 3, want 4", fake.sites.polls-polls)
	}
}

func TestSiteDeployRefusesAPathThatHoldsAQuoteABackslashOrAControlCharacter(t *testing.T) {
	for held, name := range map[string]string{
		"a double quote":      `say"hi".html`,
		"a backslash":         `back\slash.html`,
		"a control character": "bell\x07.html",
		"DEL":                 "del\x7f.html",
		"invalid UTF-8":       "latin1-\xe9.html",
	} {
		t.Run(held, func(t *testing.T) {
			fake := siteFake(t)
			dir := t.TempDir()
			writeNested(t, dir, "index.html", []byte("<p>hi</p>"))
			if err := os.WriteFile(filepath.Join(dir, name), []byte("x"), 0o644); err != nil {
				t.Skipf("this file system refuses the name, as macOS's does a non-UTF-8 one; Linux, where CI runs, allows it: %v", err)
			}

			got := fake.run(t, "site", "deploy", dir, "--app", siteApp, "--json")

			assertFailure(t, got, exitRefused, "SITE_PATH_INVALID")
			var failure struct{ Error cliError }
			decode(t, got.stdout, &failure)
			if failure.Error.Path != strings.ToValidUTF8(name, "�") || len(fake.scopes) != 0 { // JSON writes a bad byte as U+FFFD
				t.Fatalf("refusal %+v, sent %v", failure.Error, fake.operations)
			}
		})
	}
}

func TestSiteDeployRefusesWhatIsNotADirectoryOfFiles(t *testing.T) {
	for _, c := range []struct {
		name, message string
		dir           func(t *testing.T) string
	}{
		{"a missing directory", "doesn't exist", func(t *testing.T) string { return filepath.Join(t.TempDir(), "dist") }},
		{"a file", "is not a directory", func(t *testing.T) string { return writeNested(t, t.TempDir(), "index.html", []byte("<p>hi</p>")) }},
		{"an empty directory", "no files in", func(t *testing.T) string { return t.TempDir() }},
	} {
		t.Run(c.name, func(t *testing.T) {
			fake := siteFake(t)

			got := fake.run(t, "site", "deploy", c.dir(t), "--app", siteApp, "--json")

			assertFailure(t, got, exitRefused, "SITE_DIR_INVALID")
			if !strings.Contains(got.stdout, c.message) || len(fake.scopes) != 0 {
				t.Fatalf("sent %v: %s", fake.operations, got.stdout)
			}
		})
	}
}

func TestSiteDeployWaitsOnTheDefaultEnvironmentsURL(t *testing.T) {
	fake := siteFake(t)
	if created := fake.run(t, "envs", "create", "--app", siteApp, "--name", "staging", "--json"); created.exit != exitOK {
		t.Fatalf("envs create: exit %d: %s %s", created.exit, created.stdout, created.stderr)
	}
	slices.Reverse(fake.environments[siteApp])
	if listed := fake.environments[siteApp]; len(listed) != 2 || listed[0].isDefault || !listed[1].isDefault {
		t.Fatalf("the lookup must list the staging environment before the default one: %v", listed)
	}
	dir := t.TempDir()
	writeNested(t, dir, "index.html", []byte("<p>hi</p>"))

	got := fake.run(t, "site", "deploy", dir, "--app", siteApp, "--json")

	if got.exit != exitOK || !strings.Contains(got.stdout, fmt.Sprintf(`"siteUrl": %q`, fake.sites.server.URL+"/app-a-000--acme-co")) {
		t.Fatalf("exit %d: %s", got.exit, got.stdout)
	}
}

func TestSiteDeployWaitsForNoURLWhenEveryEnvironmentHasItsOwnSite(t *testing.T) {
	fake := siteFake(t)
	dir := t.TempDir()
	writeNested(t, dir, "index.html", []byte("<p>hi</p>"))
	setup := fake.run(t, "site", "deploy", dir, "--app", siteApp, "--env", "env-default", "--json")
	if setup.exit != exitOK {
		t.Fatalf("setup deploy: exit %d: %s %s", setup.exit, setup.stdout, setup.stderr)
	}
	siteWaitLimit = 20 * time.Millisecond
	polls := fake.sites.polls

	got := fake.run(t, "site", "deploy", dir, "--app", siteApp)

	if got.exit != exitOK || got.stdout != "deployment dep-4 is published (1 files, 9 bytes), but every environment has a live or disabled site of its own, so none serves it\n" {
		t.Fatalf("exit %d: %s %s", got.exit, got.stdout, got.stderr)
	}
	if fake.sites.polls != polls {
		t.Fatalf("polled %d times", fake.sites.polls-polls)
	}
}

func TestSiteDeployAsksAgainAfterAPublishGetsNoAnswerOrA5xx(t *testing.T) {
	fake := siteFake(t)
	dir := t.TempDir()
	writeNested(t, dir, "index.html", []byte("<p>hi</p>"))
	fake.sites.failPublishes = []int{502, 0}

	recovered := fake.run(t, "site", "deploy", dir, "--app", siteApp, "--json")
	publishes := fake.sites.publishCount()
	fake.sites.failPublishes = []int{503, 503, 503, 503}
	refused := fake.run(t, "site", "deploy", dir, "--app", siteApp, "--json")

	if recovered.exit != exitOK || publishes != 3 {
		t.Fatalf("exit %d, publishes %d: %s", recovered.exit, publishes, recovered.stdout)
	}
	if refused.exit != exitRefused || fake.sites.publishCount() != 7 {
		t.Fatalf("exit %d, publishes %d: %s", refused.exit, fake.sites.publishCount(), refused.stdout)
	}
}

func TestSiteDeployAsksAgainAfterAPublishAnswersTheMaskedUnexpectedError(t *testing.T) {
	fake := siteFake(t)
	dir := t.TempDir()
	writeNested(t, dir, "index.html", []byte("<p>hi</p>"))
	fake.sites.failPublishes = []int{maskedPublish, maskedPublish}

	recovered := fake.run(t, "site", "deploy", dir, "--app", siteApp, "--json")
	publishes := fake.sites.publishCount()
	fake.sites.failPublishes = []int{maskedPublish, maskedPublish, maskedPublish, maskedPublish}
	refused := fake.run(t, "site", "deploy", dir, "--app", siteApp, "--json")

	if recovered.exit != exitOK || publishes != 3 {
		t.Fatalf("exit %d, publishes %d: %s", recovered.exit, publishes, recovered.stdout)
	}
	assertFailure(t, refused, exitRefused, "INTERNAL_SERVER_ERROR")
	if fake.sites.publishCount() != 7 {
		t.Fatalf("publishes %d: %s", fake.sites.publishCount(), refused.stdout)
	}
}

func TestSiteDeployPublishesOnceWhenThePublishIsRefusedOrGetsA4xx(t *testing.T) {
	for name, setup := range map[string]func(*fakeSites){
		"a refusal":   func(s *fakeSites) { s.refuse["PublishSiteDeployment"] = map[string]any{"code": "SITE_DISABLED"} },
		"an HTTP 4xx": func(s *fakeSites) { s.failPublishes = []int{http.StatusBadRequest} },
	} {
		t.Run(name, func(t *testing.T) {
			fake := siteFake(t)
			dir := t.TempDir()
			writeNested(t, dir, "index.html", []byte("<p>hi</p>"))
			setup(fake.sites)

			got := fake.run(t, "site", "deploy", dir, "--app", siteApp, "--json")

			if got.exit == exitOK || fake.sites.publishCount() != 1 {
				t.Fatalf("exit %d, publishes %d: %s", got.exit, fake.sites.publishCount(), got.stdout)
			}
		})
	}
}

func TestSiteDeploySaysAnUploadLinkExpiredOnlyWhenS3RefusesItWith403(t *testing.T) {
	fake := siteFake(t)
	dir := t.TempDir()
	writeNested(t, dir, "index.html", []byte("<p>hi</p>"))
	fake.sites.failStatus, fake.sites.failBody = http.StatusBadRequest, "<Error><Code>BadRequest</Code><Message>Request has expired</Message></Error>"
	fake.sites.failPuts["index.html"] = 1

	got := fake.run(t, "site", "deploy", dir, "--app", siteApp, "--json")

	assertFailure(t, got, exitRefused, "UPLOAD_REFUSED")
	if strings.Contains(got.stdout, "upload link expired") {
		t.Fatalf("stdout %s", got.stdout)
	}
}

func TestSiteDeploySaysWhenAnUploadLinkHasExpired(t *testing.T) {
	fake := siteFake(t)
	dir := t.TempDir()
	writeNested(t, dir, "index.html", []byte("<p>hi</p>"))
	fake.sites.failStatus, fake.sites.failBody = 403, "<Error><Code>AccessDenied</Code><Message>Request has expired</Message></Error>"
	fake.sites.failPuts["index.html"] = 1

	got := fake.run(t, "site", "deploy", dir, "--app", siteApp, "--json")

	assertFailure(t, got, exitRefused, "UPLOAD_REFUSED")
	if !strings.Contains(got.stdout, "its upload link expired, 15 minutes after site deploy asked for it") || fake.sites.attempts["index.html"] != 1 {
		t.Fatalf("PUT %d times: %s", fake.sites.attempts["index.html"], got.stdout)
	}
}

func TestSiteStatusDoesNotSuggestPublishingWhileASiteIsDisabled(t *testing.T) {
	for _, c := range []struct {
		name, env, key string
		deploy         []string
	}{
		{"its own site", "env-new-2", "env:env-new-2", []string{"--env", "env-new-2"}},
		{"the application site", "env-default", "app:" + siteApp, nil},
	} {
		t.Run(c.name, func(t *testing.T) {
			fake := siteFake(t)
			fake.run(t, "envs", "create", "--app", siteApp, "--name", "staging", "--json")
			dir := t.TempDir()
			writeNested(t, dir, "index.html", []byte("<p>hi</p>"))
			fake.run(t, append([]string{"site", "deploy", dir, "--app", siteApp, "--json"}, c.deploy...)...)
			fake.sites.disabled[fake.sites.siteIDs[c.key]] = true

			human := fake.run(t, "site", "status", "--app", siteApp, "--env", c.env)
			asJSON := fake.run(t, "site", "status", "--app", siteApp, "--env", c.env, "--json")

			if !strings.Contains(human.stdout, "serves no site at") || !strings.Contains(human.stdout, "MultiPaaS has disabled a site of this application, so none can be published") || strings.Contains(human.stdout, "publish one") {
				t.Fatalf("stdout %q", human.stdout)
			}
			if !strings.Contains(compact(asJSON.stdout), `"source":"none","site":null,"disabled":true`) {
				t.Fatalf("status %s", asJSON.stdout)
			}
		})
	}
}

func TestSiteStatusSaysASiteIsDisabledWhileItServesAnother(t *testing.T) {
	fake := siteFake(t)
	fake.run(t, "envs", "create", "--app", siteApp, "--name", "staging", "--json")
	dir := t.TempDir()
	writeNested(t, dir, "index.html", []byte("<p>hi</p>"))
	fake.run(t, "site", "deploy", dir, "--app", siteApp, "--json")
	fake.run(t, "site", "deploy", dir, "--app", siteApp, "--env", "env-new-2", "--json")
	fake.sites.disabled[fake.sites.siteIDs["env:env-new-2"]] = true

	human := fake.run(t, "site", "status", "--app", siteApp, "--env", "env-default")

	if !strings.Contains(human.stdout, "serves the application's site") || !strings.Contains(human.stdout, "MultiPaaS has disabled a site of this application, so none of its sites can be published") {
		t.Fatalf("stdout %q", human.stdout)
	}
}

func TestSiteCommandsTakeTheEnvironmentFromMultipaasJSON(t *testing.T) {
	fake := siteFake(t)
	fake.run(t, "envs", "create", "--app", siteApp, "--name", "staging", "--json")
	t.Chdir(t.TempDir())
	if err := os.WriteFile(projectFile, []byte(`{"applicationId": "app-a-000", "environmentId": "env-new-2"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	writeNested(t, dir, "index.html", []byte("<p>hi</p>"))

	own := fake.run(t, "site", "deploy", dir, "--json")
	ownSent := fake.mutations[len(fake.mutations)-2]
	appSite := fake.run(t, "site", "deploy", dir, "--app-site", "--json")
	appSent := fake.mutations[len(fake.mutations)-2]
	status := fake.run(t, "site", "status", "--json")
	removed := fake.run(t, "site", "remove", "--json")

	if own.exit != exitOK || ownSent["environmentId"] != "env-new-2" {
		t.Fatalf("exit %d, sent %v: %s", own.exit, ownSent, own.stdout)
	}
	if _, named := appSent["environmentId"]; appSite.exit != exitOK || named {
		t.Fatalf("exit %d, sent %v: %s", appSite.exit, appSent, appSite.stdout)
	}
	if !strings.Contains(compact(status.stdout), `"environmentId":"env-new-2"`) || compact(removed.stdout) != `{"removed":true,"id":"env-new-2"}` {
		t.Fatalf("status %s, remove %s", status.stdout, removed.stdout)
	}
}

func TestSiteRemoveTakesAnEnvironmentBackToTheApplicationsSite(t *testing.T) {
	fake := siteFake(t)
	fake.run(t, "envs", "create", "--app", siteApp, "--name", "staging", "--json")
	dir := t.TempDir()
	writeNested(t, dir, "index.html", []byte("<p>hi</p>"))
	fake.run(t, "site", "deploy", dir, "--app", siteApp, "--json")
	fake.run(t, "site", "deploy", dir, "--app", siteApp, "--env", "env-new-2", "--json")

	removed := fake.run(t, "site", "remove", "--app", siteApp, "--env", "env-new-2", "--json")
	again := fake.run(t, "site", "remove", "--app", siteApp, "--env", "env-new-2", "--json")

	if compact(removed.stdout) != `{"removed":true,"id":"env-new-2"}` || compact(again.stdout) != compact(removed.stdout) {
		t.Fatalf("stdout %s, then %s", removed.stdout, again.stdout)
	}
	status := fake.run(t, "site", "status", "--app", siteApp, "--env", "env-new-2", "--json")
	var shown siteState
	decode(t, status.stdout, &shown)
	if shown.Source != "application" || shown.Site == nil || shown.Site.Id != "site-1" {
		t.Fatalf("status %s", status.stdout)
	}
}

func TestSiteRemoveWithoutEnvNeedsYesThenDeletesTheApplicationsSite(t *testing.T) {
	fake := siteFake(t)
	dir := t.TempDir()
	writeNested(t, dir, "index.html", []byte("<p>hi</p>"))
	fake.run(t, "site", "deploy", dir, "--app", siteApp, "--json")
	sent := len(fake.mutations)

	refused := fake.run(t, "site", "remove", "--app", siteApp, "--json")
	assertFailure(t, refused, exitRefused, "CONFIRMATION_REQUIRED")
	if len(fake.mutations) != sent {
		t.Fatalf("sent %v", fake.mutations[sent:])
	}
	deleted := fake.run(t, "site", "remove", "--app", siteApp, "--yes", "--json")

	if compact(deleted.stdout) != `{"removed":true,"id":"app-a-000"}` || !reflect.DeepEqual(fake.mutations[len(fake.mutations)-1], map[string]any{"applicationId": siteApp}) {
		t.Fatalf("stdout %s, sent %v", deleted.stdout, fake.mutations[len(fake.mutations)-1])
	}
	status := fake.run(t, "site", "status", "--app", siteApp, "--json")
	if !strings.Contains(compact(status.stdout), `"source":"none","site":null`) {
		t.Fatalf("status %s", status.stdout)
	}
}

// longPaths writes n files whose paths are 256 bytes each, so 1280 of them total exactly 320 KiB, and makes the last
// path extra bytes longer.
func longPaths(t *testing.T, dir string, n, extra int) {
	t.Helper()
	for i := range n {
		name := fmt.Sprintf("d/%04d%s", i, strings.Repeat("x", 250))
		if i == n-1 {
			name += strings.Repeat("y", extra)
		}
		writeNested(t, dir, name, []byte("x"))
	}
}

// sparse writes a file of size bytes that takes no space on disk.
func sparse(t *testing.T, dir, name string, size int64) {
	t.Helper()
	path := writeNested(t, dir, name, nil)
	if err := os.Truncate(path, size); err != nil {
		t.Fatal(err)
	}
}
