//go:build wasip1

package root

import (
	"github.com/cli/cli/v2/pkg/cmdutil"
	"github.com/spf13/cobra"
)

// Two commands are left out of the WebAssembly build.
//
//   - codespace needs a terminal UI (tcell) and an SSH tunnel to the codespace,
//     neither of which exists in the browser.
//   - attestation verifies sigstore bundles, which drags in a trust root fetched
//     over TLS from a service the guest cannot reach, and adds tens of megabytes
//     to a module that already has to be downloaded by a browser.
//
// Leaving them out is what keeps their dependencies (tcell, dev-tunnels,
// sigstore, in-toto) out of the module entirely, so those never have to be
// taught about wasip1.

func attestationCommand(*cmdutil.Factory) *cobra.Command { return nil }

func codespaceCommand(*cmdutil.Factory) *cobra.Command { return nil }
