package publish

import (
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bluesky-social/indigo/atproto/atdata"

	"github.com/isabelroses/snot/internal/testutil"
)

func gitIn(tb testing.TB, dir, stdin string, args ...string) string {
	tb.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Stdin = strings.NewReader(stdin)
	out, err := cmd.CombinedOutput()
	if err != nil {
		tb.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return string(out)
}

// refsFixture returns a bare repo holding n extra branches on top of the
// fixture's main, either loose or packed the way `git gc` leaves them.
func refsFixture(tb testing.TB, n int, packed bool) string {
	tb.Helper()
	bare := filepath.Join(testutil.FixtureRepo(tb, "isabel", "demo"), "isabel", "demo.git")
	head := strings.TrimSpace(gitIn(tb, bare, "", "rev-parse", "main"))
	var updates strings.Builder
	for i := range n {
		fmt.Fprintf(&updates, "create refs/heads/b%05d %s\n", i, head)
	}
	gitIn(tb, bare, updates.String(), "update-ref", "--stdin")
	if packed {
		gitIn(tb, bare, "", "pack-refs", "--all")
	}
	return bare
}

func refsOf(t *testing.T, path string) (string, bool) {
	t.Helper()
	rec, err := refsRecord(path, "main")
	if err != nil {
		t.Fatal(err)
	}
	if rec == nil {
		return "", false
	}
	return string(rec.Value["refs"].(atdata.Bytes)), true
}

// TestRefsRecordMatchesGit holds the snapshot to git's own listing across
// packed refs, loose refs, a loose ref shadowing its packed entry, an
// annotated tag, and the namespaces that must stay out.
func TestRefsRecordMatchesGit(t *testing.T) {
	bare := refsFixture(t, 300, true)
	second := strings.TrimSpace(gitIn(t, bare, "", "-c", "user.name=t", "-c", "user.email=t@t", "commit-tree", "-m", "second", "main^{tree}", "-p", "main"))
	gitIn(t, bare, "", "update-ref", "refs/heads/b00007", second)
	gitIn(t, bare, "", "update-ref", "refs/heads/Zeta", second)
	gitIn(t, bare, "", "update-ref", "refs/heads/a/nested", second)
	gitIn(t, bare, "", "update-ref", "refs/notes/commits", second)
	gitIn(t, bare, "", "-c", "user.name=t", "-c", "user.email=t@t", "tag", "-a", "-m", "v1", "v1", "main")
	for _, hidden := range []string{"refs/pull/1/head", "refs/hidden/x", "refs/cobs/x", "refs/cob-checkpoints/x", "refs/atproto/x", "refs/tngl/x"} {
		gitIn(t, bare, "", "update-ref", hidden, second)
	}

	var want strings.Builder
	for _, line := range strings.SplitAfter(gitIn(t, bare, "", "for-each-ref", "--format=%(objectname) %(refname)"), "\n") {
		if _, name, ok := strings.Cut(strings.TrimSuffix(line, "\n"), " "); ok && publicRef(name) {
			want.WriteString(line)
		}
	}
	if n := strings.Count(want.String(), "\n"); n != 305 {
		t.Fatalf("fixture lists %d public refs, want 305", n)
	}
	got, ok := refsOf(t, bare)
	if !ok {
		t.Fatal("no refs record")
	}
	if got != want.String() {
		t.Errorf("refs differ from git for-each-ref:\ngot  %d bytes\nwant %d bytes", len(got), want.Len())
	}
}

// TestRefsRecordSizeLimit pins the cut-off: a snapshot of exactly
// maxRefsBytes is published whole, one byte more publishes nothing.
func TestRefsRecordSizeLimit(t *testing.T) {
	const n = 16946 // 59-byte lines; main's is 57
	bare := refsFixture(t, n, true)
	head := strings.TrimSpace(gitIn(t, bare, "", "rev-parse", "main"))
	pad := maxRefsBytes - 57 - 59*n - 42 // name length that fills the rest
	name := "refs/heads/" + strings.Repeat("p", pad-len("refs/heads/"))
	gitIn(t, bare, "", "update-ref", name, head)

	got, ok := refsOf(t, bare)
	if !ok || len(got) != maxRefsBytes {
		t.Fatalf("at the limit: published=%v, %d bytes; want all %d", ok, len(got), maxRefsBytes)
	}
	gitIn(t, bare, "", "update-ref", "-d", name)
	gitIn(t, bare, "", "update-ref", name+"p", head)
	if got, ok := refsOf(t, bare); ok {
		t.Fatalf("one byte past the limit published %d bytes", len(got))
	}
}

// BenchmarkRefsRecord covers the per-sync cost of snapshotting a repo's
// refs. 16k branches is just under maxRefsBytes; 20k is past it, the case
// that publishes no record.
func BenchmarkRefsRecord(b *testing.B) {
	for _, bc := range []struct {
		name   string
		refs   int
		packed bool
	}{
		{"refs=10/loose", 10, false},
		{"refs=10/packed", 10, true},
		{"refs=16000/loose", 16000, false},
		{"refs=16000/packed", 16000, true},
		{"refs=20000/packed", 20000, true},
	} {
		b.Run(bc.name, func(b *testing.B) {
			path := refsFixture(b, bc.refs, bc.packed)
			b.ReportAllocs()
			for b.Loop() {
				if _, err := refsRecord(path, "main"); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
