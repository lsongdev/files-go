package store

import "os"

// deviceOf is unknown on Windows; empty roots are then only accepted when
// a scan is forced.
func deviceOf(os.FileInfo) uint64 { return 0 }
