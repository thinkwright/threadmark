package buildinfo

// Version is the source-level Threadmark version.
//
// Release preparation stamps this file before tagging so `go install
// github.com/thinkwright/threadmark/cmd/threadmark@vX.Y.Z` reports the release
// version. GoReleaser also stamps this variable with the tag-derived version
// when building release archives.
var Version = "0.1.0-dev"
