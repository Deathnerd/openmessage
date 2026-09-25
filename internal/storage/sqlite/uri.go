package sqlite

import (
	"net/url"
	"strings"
)

// FileURI builds a SQLite file: URI for path with the given raw query. A
// Windows drive path (C:\x or C:/x) becomes file:///C:/x, the form SQLite
// accepts; relative and Unix paths are left as they are.
func FileURI(path, rawQuery string) string {
	normalizedPath := strings.ReplaceAll(path, `\`, "/")
	if isWindowsAbsolutePath(normalizedPath) {
		normalizedPath = "/" + normalizedPath
	}
	return (&url.URL{Scheme: "file", Path: normalizedPath, RawQuery: rawQuery}).String()
}

func isWindowsAbsolutePath(path string) bool {
	if len(path) < 3 {
		return false
	}
	drive := path[0]
	return ((drive >= 'a' && drive <= 'z') || (drive >= 'A' && drive <= 'Z')) &&
		path[1] == ':' && path[2] == '/'
}
