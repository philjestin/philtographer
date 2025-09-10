package cmd

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

// cfgFile stores an optional explicit path to a config file
// (if not provided we try ./philtographer.config.json by default).
var cfgFile string

// workspace (aka --root) and outputFile (aka --out) mirror your old flags.
var workspace string
var outputFile string

var rootCmd = &cobra.Command{
	Use:   "philtographer",
	Short: "Code graph & impact analysis for monorepos",
	// PersistentPreRunE executes before any subcommand; we use it to load config/env.
	PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
		// If --config was provided, take it; else look for ./philtographer.config.{json,yaml,toml}
		if cfgFile != "" {
			viper.SetConfigFile(cfgFile)
		} else {
			viper.AddConfigPath(".")
			viper.SetConfigName("philtographer.config")
			// Let viper detect the extension (json/yaml/toml) automatically.
		}

		// Read env vars with prefix PHILTOGRAPHER_, e.g. PHILTOGRAPHER_ROOT
		viper.SetEnvPrefix("PHILTOGRAPHER")
		viper.AutomaticEnv()

		// Read config file if present; it's ok if none is found.
		if err := viper.ReadInConfig(); err == nil {
			fmt.Fprintln(os.Stderr, "Using config file:", viper.ConfigFileUsed())
		}
		return nil
	},
}

// Execute is called from main.go and starts the CLI.
func Execute() {
	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func init() {
	// Define persistent flags that apply to all subcommands.
	rootCmd.PersistentFlags().StringVar(&cfgFile, "config", "", "config file (default: ./philtographer.config.{json,yaml,toml})")
	rootCmd.PersistentFlags().StringVar(&workspace, "root", ".", "repo root to scan")
	rootCmd.PersistentFlags().StringVar(&outputFile, "out", "", "write graph JSON to file")

	// Bind these flags to viper keys so config/env/flags merge cleanly.
	_ = viper.BindPFlag("root", rootCmd.PersistentFlags().Lookup("root"))
	_ = viper.BindPFlag("out", rootCmd.PersistentFlags().Lookup("out"))
}
