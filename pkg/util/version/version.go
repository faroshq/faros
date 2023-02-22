package version

var version = "?" // set by ldflags

type Version struct {
	Version string `json:"version"`
}

func GetVersion() *Version {
	return &Version{
		Version: version,
	}
}
