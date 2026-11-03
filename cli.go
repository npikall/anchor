package main

import "github.com/spf13/cobra"

type config struct {
	update  bool
	inPlace bool
	verbose bool
	workers int
}

func newRootCmd() *cobra.Command {
	var cfg config
	cmd := &cobra.Command{
		Use:   "anchor [flags] <file-or-directory>...",
		Short: "Pin GitHub Actions uses: directives to their exact commit SHA",
		Long: "Pin GitHub Actions uses: directives to their exact commit SHA.\n\n" +
			"Directories are searched recursively for .yml and .yaml files.",
		Args: cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := setupGithubClient()
			if err != nil {
				return err
			}
			return run(cmd.Context(), client, cfg, args, cmd.OutOrStdout())
		},
	}
	flags := cmd.Flags()
	flags.BoolVarP(&cfg.update, "update", "u", false, "pin to latest tag instead of current ref")
	flags.BoolVarP(&cfg.inPlace, "in-place", "i", false, "overwrite files in place")
	flags.BoolVarP(&cfg.verbose, "verbose", "v", false, "warn about skipped local/docker actions")
	flags.IntVarP(&cfg.workers, "workers", "n", 1, "number of concurrent workers")
	return cmd
}
