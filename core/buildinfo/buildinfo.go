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

// Unknown is what a field reports when the build was not stamped with it.
const Unknown = "unknown"

// Set with: -ldflags "-X github.com/.../core/buildinfo.Commit=<sha> ..."
// The Dockerfile stamps both from its SQC_COMMIT and SQC_IMAGE_TAG build args.
var (
	// Commit is the full git SHA the binary was built from.
	Commit = Unknown

	// ImageTag is the container tag published for this build, e.g. "v0.4.9".
	// Distinct from Commit: a tag says which artefact was rolled out, a commit
	// says what is in it, and reconciling a deploy needs both.
	ImageTag = Unknown
)

// Version is the one string a node presents wherever a single "version" is
// expected: /status and /healthz node_version, the P2P announcement, the
// Prometheus label. It is derived from the stamped identity, never declared:
// the image tag when the build has one, else the short commit, else Unknown.
//
// There is deliberately no release literal to fall back to, and no variable
// for a build to stamp one into. A hardcoded "1.0.0-quantum" is how every
// build of every commit came to report the same version.
func Version() string {
	if stamped(ImageTag) {
		return ImageTag
	}
	if stamped(Commit) {
		return ShortCommit()
	}
	return Unknown
}

// stamped reports whether a build-identity value was actually injected. The
// Dockerfile defaults its build args to "unknown", and a shell variable that
// was never set arrives through -X as "", so both read as absent.
func stamped(v string) bool {
	return v != "" && v != Unknown
}

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
		"version":   Version(),
		"commit":    Commit,
		"image_tag": ImageTag,
	}
}
