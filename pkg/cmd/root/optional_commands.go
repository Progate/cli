//go:build !wasip1

package root

import (
	attestationCmd "github.com/cli/cli/v2/pkg/cmd/attestation"
	codespaceCmd "github.com/cli/cli/v2/pkg/cmd/codespace"
	"github.com/cli/cli/v2/pkg/cmdutil"
	"github.com/spf13/cobra"
)

// Commands that not every build of gh can carry. On a normal platform that is
// all of them; see the wasip1 twin of this file for the ones a WebAssembly
// build leaves out and why.

func attestationCommand(f *cmdutil.Factory) *cobra.Command {
	return attestationCmd.NewCmdAttestation(f)
}

func codespaceCommand(f *cmdutil.Factory) *cobra.Command {
	return codespaceCmd.NewCmdCodespace(f)
}
