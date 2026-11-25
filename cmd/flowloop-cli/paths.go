package main

import (
	"fmt"

	"github.com/Sinkear/flowloop/internal/paths"
)

// pathsCmd prints the resolved machine-side layout. The daemon has the same
// command (`flowloop-daemon paths`) so operators can compare the host and the
// machine side of the link; both honor FLOWLOOP_HOME.
func pathsCmd() {
	fmt.Println(paths.Current().JSON())
}
