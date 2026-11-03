package main

import "github.com/spf13/cobra"

type config struct {
	update  bool
	inPlace bool
	verbose bool
	refresh bool
	workers int
}

func newRootCmd() *cobra.Command {
	var cfg config
	cmd := &cobra.Command{
		Use:   "anchor [flags] <file-or-directory>...",
		Short: "Anchor GitHub Actions to their exact commit SHA",
		Long: `Anchor GitHub Actions to their exact commit SHA.
Directories are searched recursively for .yml and .yaml files.`,
		Args: cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := setupGithubClient()
			if err != nil {
				return err
			}
			store := openDefaultCache(cmd.Context(), cmd.ErrOrStderr())
			defer store.close()
			return run(cmd.Context(), client, store, cfg, args, cmd.OutOrStdout(), cmd.ErrOrStderr())
		},
	}
	flags := cmd.Flags()
	flags.BoolVarP(&cfg.update, "update", "u", false, "pin to latest tag instead of current ref")
	flags.BoolVarP(&cfg.inPlace, "in-place", "i", false, "overwrite files in place")
	flags.BoolVarP(&cfg.verbose, "verbose", "v", false, "warn about skipped local/docker actions")
	flags.BoolVarP(&cfg.refresh, "refresh", "r", false, "request all actions again instead of reusing cached hashes")
	flags.IntVarP(&cfg.workers, "workers", "n", 1, "number of concurrent workers")
	return cmd
}
