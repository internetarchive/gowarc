package main

import (
	"log/slog"
	"os"

	"github.com/internetarchive/gowarc/cmd/extract"
	"github.com/internetarchive/gowarc/cmd/mend"
	"github.com/internetarchive/gowarc/cmd/verify"
	"github.com/spf13/cobra"
)

func init() {
	// Add global flags
	rootCmd.PersistentFlags().Bool("json", false, "Output logs in JSON format")
	rootCmd.PersistentFlags().BoolP("verbose", "v", false, "Enable verbose/debug logging")

	// Setup logger before adding subcommands
	rootCmd.PersistentPreRun = func(cmd *cobra.Command, args []string) {
		setupLogger(cmd)
	}

	rootCmd.AddCommand(extract.Command)
	rootCmd.AddCommand(mend.Command)
	rootCmd.AddCommand(verify.Command)
}

// setupLogger configures the global logger based on flags
func setupLogger(cmd *cobra.Command) {
	jsonOutput, _ := cmd.Flags().GetBool("json")
	verbose, _ := cmd.Flags().GetBool("verbose")

	var handler slog.Handler
	if jsonOutput {
		handler = slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
			Level: getLogLevel(verbose),
		})
	} else {
		handler = slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{
			Level: getLogLevel(verbose),
		})
	}

	logger := slog.New(handler)
	slog.SetDefault(logger)
}

// getLogLevel returns the appropriate log level based on verbose flag
func getLogLevel(verbose bool) slog.Level {
	if verbose {
		return slog.LevelDebug
	}
	return slog.LevelInfo
}

// rootCmd represents the base command when called without any subcommands
var rootCmd = &cobra.Command{
	Use:   "cmd",
	Short: "Utility to process WARC files",
	Long:  `Utility to process WARC files`,
}

func main() {
	err := rootCmd.Execute()
	if err != nil {
		os.Exit(1)
	}
}
