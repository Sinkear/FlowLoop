package main

import (
	"fmt"

	"github.com/Sinkear/ouroflow/internal/paths"
)

// pathsCmd prints the resolved machine-side layout. The daemon has the same
// command (`ouroflow-daemon paths`) so operators can compare the host and the
// machine side of the link; both honor OUROFLOW_HOME.
func pathsCmd() {
	fmt.Println(paths.Current().JSON())
}
