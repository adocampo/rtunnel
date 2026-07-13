package cmd

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

var cfgFile string

var rootCmd = &cobra.Command{
	Use:   "rtunnel",
	Short: "Reverse network tunnel — expose private machines to your network",
	Long: `rtunnel creates reverse network tunnels. A machine inside a private network
(WSL, Docker container, cloud VM) initiates an outbound WebSocket connection
to your machine, giving you transparent IP-level access via a TUN interface
or a SOCKS5 proxy.`,
}

func Execute() {
	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func init() {
	cobra.OnInitialize(initConfig)
	rootCmd.PersistentFlags().StringVar(&cfgFile, "config", "", "config file (default: ./rtunnel.yaml)")
	rootCmd.PersistentFlags().BoolP("verbose", "v", false, "enable verbose logging")
	viper.BindPFlag("verbose", rootCmd.PersistentFlags().Lookup("verbose"))
}

func initConfig() {
	if cfgFile != "" {
		viper.SetConfigFile(cfgFile)
	} else {
		viper.SetConfigName("rtunnel")
		viper.SetConfigType("yaml")
		viper.AddConfigPath(".")
		viper.AddConfigPath("$HOME/.config/rtunnel")
		viper.AddConfigPath("/etc/rtunnel")
	}

	viper.AutomaticEnv()
	viper.SetEnvPrefix("RTUNNEL")

	if err := viper.ReadInConfig(); err == nil {
		if viper.GetBool("verbose") {
			fmt.Fprintln(os.Stderr, "Using config file:", viper.ConfigFileUsed())
		}
	}
}
