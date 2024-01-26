package cmd

import (
	"fmt"
	"plandex/api"
	"plandex/auth"
	"plandex/lib"
	"plandex/term"

	"github.com/spf13/cobra"
)

// logCmd represents the log command
var logCmd = &cobra.Command{
	Use:     "log",
	Aliases: []string{"history", "logs"},
	Short:   "Show plan history",
	Long:    `Show plan history.`,
	Args:    cobra.NoArgs,
	Run:     runLog,
}

func init() {
	// Add log command
	RootCmd.AddCommand(logCmd)
}

func runLog(cmd *cobra.Command, args []string) {
	auth.MustResolveAuthWithOrg()
	lib.MustResolveProject()

	if lib.CurrentPlanId == "" {
		fmt.Println("No current plan")
		return
	}

	res, err := api.Client.ListLogs(lib.CurrentPlanId, lib.CurrentBranch)
	if err != nil {
		fmt.Printf("Error: %v\n", err)
		return
	}

	term.PageOutput(res.Body)
}
