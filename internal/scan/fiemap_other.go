//go:build !linux

package scan

import "errors"

// errFiemapUnsupported is returned on platforms without the Linux FIEMAP
// interface. sharedExtentFS also reports false there, so the pass never runs.
var errFiemapUnsupported = errors.New("scan: fiemap unsupported on this platform")

func sharedExtentFS(string) bool { return false }

func fiemapFile(string) (fileInfoExtents, error) { return fileInfoExtents{}, errFiemapUnsupported }
