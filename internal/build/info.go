package build

// Identity stamps. A plain `go build` leaves the defaults: a binary
// that cannot name its commit must not pretend it can.
var (
	version = "dev"
	commit  = "none"
	date    = "unknown"
)

func Version() string { return version }
func Commit() string  { return commit }
func Date() string    { return date }

// Line is the `nabd --version` output.
func Line() string {
	return version + " · " + commit + " · " + date
}

// BannerPrefix is the RunStart identity before provider and path.
func BannerPrefix() string {
	return "nabd " + version + " · " + commit + " · " + date
}
