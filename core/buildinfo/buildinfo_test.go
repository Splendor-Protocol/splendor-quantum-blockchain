package buildinfo

import "testing"

const fullSHA = "3dc5a35e0b6d4a3f9c2e1d0f8a7b6c5d4e3f2a1b"

// stamp sets the link-time values for one test and restores them afterwards.
func stamp(t *testing.T, commit, imageTag string) {
	t.Helper()
	prevCommit, prevTag := Commit, ImageTag
	Commit, ImageTag = commit, imageTag
	t.Cleanup(func() { Commit, ImageTag = prevCommit, prevTag })
}

// The tag names the artefact that was rolled out, so it wins when present.
func TestVersionPrefersImageTag(t *testing.T) {
	stamp(t, fullSHA, "v1.1.0-devnet")
	if got := Version(); got != "v1.1.0-devnet" {
		t.Fatalf("Version() = %q, want the image tag", got)
	}
}

// A build stamped with a commit but no tag — the Dockerfile's default arg is
// the literal "unknown", an unset shell variable arrives as "" — reports the
// commit at git display length, the same form the startup log uses.
func TestVersionFallsBackToShortCommit(t *testing.T) {
	for _, tag := range []string{"", Unknown} {
		stamp(t, fullSHA, tag)
		if got := Version(); got != fullSHA[:12] {
			t.Fatalf("ImageTag=%q: Version() = %q, want %q", tag, got, fullSHA[:12])
		}
	}
}

// An un-stamped build says so. There is no release literal to fall back to:
// that literal is how every build of every commit reported "1.0.0-quantum".
func TestVersionUnstampedIsUnknown(t *testing.T) {
	for _, v := range []string{"", Unknown} {
		stamp(t, v, v)
		if got := Version(); got != Unknown {
			t.Fatalf("Commit=ImageTag=%q: Version() = %q, want %q", v, got, Unknown)
		}
	}
}

func TestMapVersionMatchesVersion(t *testing.T) {
	stamp(t, fullSHA, "")
	m := Map()
	if m["version"] != Version() || m["commit"] != fullSHA || m["image_tag"] != "" {
		t.Fatalf("Map() = %v, want version %q, commit %q, image_tag \"\"", m, Version(), fullSHA)
	}
}
