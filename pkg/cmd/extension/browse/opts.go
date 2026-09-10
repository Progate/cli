package browse

// The options ExtBrowse takes, kept apart from the implementation because that
// implementation only exists on platforms with a terminal (see browse.go and
// its wasip1 twin).

import (
	"log"
	"net/http"

	"github.com/cli/cli/v2/internal/gh"
	"github.com/cli/cli/v2/pkg/extensions"
	"github.com/cli/cli/v2/pkg/iostreams"
	"github.com/cli/cli/v2/pkg/search"
	"github.com/spf13/cobra"
)

type ExtBrowseOpts struct {
	Cmd          *cobra.Command
	Browser      ibrowser
	IO           *iostreams.IOStreams
	Searcher     search.Searcher
	Em           extensions.ExtensionManager
	Client       *http.Client
	Logger       *log.Logger
	Cfg          gh.Config
	Rg           *readmeGetter
	Debug        bool
	SingleColumn bool
}

type ibrowser interface {
	Browse(string) error
}
