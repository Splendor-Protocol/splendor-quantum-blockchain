// Package buildinfo carries the identity of the binary that is running.
//
// It exists because "which code is actually deployed?" was not answerable from
// the outside. node_version was the string literal "1.0.0-quantum" in two
// handlers, so every build of every commit reported the same thing, and the only
// way to tell what a tier ran was to find the build's log and read the checkout
// line. On 27 Aug main was force-pushed backwards and the running tiers kept
// serving code that no longer existed in the repository — and nothing they
// served said so.
//
// Values are injected at link time. An un-stamped build says "unknown" rather
// than guessing, because a wrong commit is worse than an absent one.
package buildinfo

// Set with: -ldflags "-X github.com/.../core/buildinfo.Commit=<sha> ..."
var (
	// Version is the release identity, e.g. "1.0.0-quantum".
	Version = "1.0.0-quantum"

	// Commit is the full git SHA the binary was built from.
	Commit = "unknown"

	// ImageTag is the container tag published for this build, e.g. "v0.4.9".
	// Distinct from Commit: a tag says which artefact was rolled out, a commit
	// says what is in it, and reconciling a deploy needs both.
	ImageTag = "unknown"
)

// ShortCommit returns the first 12 characters of Commit, the length git uses
// for display. Returns Commit unchanged when it is not a full SHA.
func ShortCommit() string {
	if len(Commit) < 12 {
		return Commit
	}
	return Commit[:12]
}

// Map returns the build identity for embedding in an RPC response.
func Map() map[string]any {
	return map[string]any{
		"version":   Version,
		"commit":    Commit,
		"image_tag": ImageTag,
	}
}
