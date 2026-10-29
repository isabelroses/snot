package xrpc

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"
)

func getJSON(t *testing.T, srv *httptest.Server, nsid string, params url.Values) (int, map[string]any) {
	t.Helper()
	u := srv.URL + "/" + nsid
	if params != nil {
		u += "?" + params.Encode()
	}
	res, err := http.Get(u)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	body, _ := io.ReadAll(res.Body)
	var m map[string]any
	if len(body) > 0 {
		if err := json.Unmarshal(body, &m); err != nil {
			t.Fatalf("bad json (%d): %s", res.StatusCode, body)
		}
	}
	return res.StatusCode, m
}

func TestOwner(t *testing.T) {
	srv := httptest.NewServer(newXrpc(t).Router())
	defer srv.Close()
	code, m := getJSON(t, srv, "sh.tangled.owner", nil)
	if code != 200 || m["owner"] != "did:plc:knotadmin" {
		t.Errorf("code=%d m=%v", code, m)
	}
}

func TestListKeys(t *testing.T) {
	srv := httptest.NewServer(newXrpc(t).Router())
	defer srv.Close()
	code, m := getJSON(t, srv, "sh.tangled.knot.listKeys", nil)
	if code != 200 {
		t.Fatalf("code=%d", code)
	}
	keys := m["keys"].([]any)
	k := keys[0].(map[string]any)
	if k["did"] != "did:plc:owner123" || k["key"] != "ssh-ed25519 AAAA test" {
		t.Errorf("key = %v", k)
	}
}

func TestLanguages(t *testing.T) {
	srv := httptest.NewServer(newXrpc(t).Router())
	defer srv.Close()
	code, m := getJSON(t, srv, "sh.tangled.repo.languages",
		url.Values{"repo": {"did:plc:owner123/demo"}})
	if code != 200 {
		t.Fatalf("code=%d m=%v", code, m)
	}
	langs := m["languages"].([]any)
	if len(langs) != 2 {
		t.Errorf("languages = %v", langs)
	}
	if m["totalSize"].(float64) != 1500 {
		t.Errorf("totalSize = %v", m["totalSize"])
	}
}

func TestDescribeRepo(t *testing.T) {
	x := newXrpc(t)
	if err := x.Rkeys.Put("3l5tidrkey", "demo"); err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(x.Router())
	defer srv.Close()

	code, m := getJSON(t, srv, "sh.tangled.repo.describeRepo",
		url.Values{"repoDid": {"did:plc:repo123"}})
	if code != 200 {
		t.Fatalf("code=%d m=%v", code, m)
	}
	if m["ownerDid"] != "did:plc:owner123" || m["rkey"] != "3l5tidrkey" {
		t.Errorf("m=%v", m)
	}

	code, _ = getJSON(t, srv, "sh.tangled.repo.describeRepo",
		url.Values{"repoDid": {"did:plc:unknowndid"}})
	if code != 404 {
		t.Errorf("missing repo code=%d", code)
	}
}

func TestReadHandlersSmoke(t *testing.T) {
	srv := httptest.NewServer(newXrpc(t).Router())
	defer srv.Close()
	repo := url.Values{"repo": {"did:plc:owner123/demo"}}

	for _, tc := range []struct {
		nsid   string
		params url.Values
	}{
		{"sh.tangled.repo.getDefaultBranch", repo},
		{"sh.tangled.repo.branches", repo},
		{"sh.tangled.repo.tags", repo},
		{"sh.tangled.repo.tree", url.Values{"repo": repo["repo"], "ref": {"main"}}},
		{"sh.tangled.repo.log", url.Values{"repo": repo["repo"], "ref": {"main"}}},
		{"sh.tangled.repo.blob", url.Values{"repo": repo["repo"], "ref": {"main"}, "path": {"README.md"}}},
	} {
		code, m := getJSON(t, srv, tc.nsid, tc.params)
		if code != 200 {
			t.Errorf("%s: code=%d body=%v", tc.nsid, code, m)
		}
	}

	resp, err := http.Get(srv.URL + "/sh.tangled.repo.archive?" +
		url.Values{"repo": repo["repo"], "ref": {"main"}, "format": {"tar.gz"}}.Encode())
	if err != nil {
		t.Fatal(err)
	}
	archive, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	// gzip magic: the archive actually streamed rather than failing mid-body
	if resp.StatusCode != 200 || len(archive) < 2 || archive[0] != 0x1f || archive[1] != 0x8b {
		t.Errorf("archive: code=%d len=%d", resp.StatusCode, len(archive))
	}
	if !strings.Contains(resp.Header.Get("Link"), `rel="immutable"`) {
		t.Errorf("archive Link=%q", resp.Header.Get("Link"))
	}

	// raw blobs are repo-controlled content served on the knot's origin
	resp, err = http.Get(srv.URL + "/sh.tangled.repo.blob?" +
		url.Values{"repo": repo["repo"], "ref": {"main"}, "path": {"README.md"}, "raw": {"true"}}.Encode())
	if err != nil {
		t.Fatal(err)
	}
	rawBlob, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 200 || len(rawBlob) == 0 || resp.Header.Get("Content-Length") != strconv.Itoa(len(rawBlob)) {
		t.Errorf("raw blob: code=%d len=%d content-length=%q", resp.StatusCode, len(rawBlob), resp.Header.Get("Content-Length"))
	}
	if resp.Header.Get("X-Content-Type-Options") != "nosniff" || !strings.Contains(resp.Header.Get("Content-Security-Policy"), "sandbox") {
		t.Errorf("raw blob missing nosniff/CSP: %v", resp.Header)
	}

	code, _ := getJSON(t, srv, "sh.tangled.repo.branches",
		url.Values{"repo": {"did:plc:owner123/missing"}})
	if code == 200 {
		t.Error("missing repo should not 200")
	}
}
