//go:build !ios

package main

// visibleStorageDir reports that there is nowhere the user can browse to.
//
// Desktop downloads go wherever the save dialog said, so the question does not
// arise. Android has a real public Downloads folder, but nothing in the app
// sandbox is visible to its Files app and the app cannot write outside the
// sandbox from Go, so the move out happens on the host side instead.
func visibleStorageDir() string { return "" }
