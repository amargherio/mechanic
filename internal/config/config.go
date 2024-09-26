package config

import (
	"context"
	"github.com/amargherio/mechanic/internal/appstate"
	"github.com/spf13/viper"

	"go.uber.org/zap"
	"k8s.io/client-go/rest"
)

// DrainConditions is a struct that holds the VM scheduled event types that would trigger a drain
type DrainConditions struct {
	DrainOnFreeze    bool
	DrainOnReboot    bool
	DrainOnRedeploy  bool
	DrainOnPreempt   bool
	DrainOnTerminate bool
}

// ContextValues is a struct that holds the logger and state of the application for use in the shared application context
type ContextValues struct {
	Logger *zap.SugaredLogger
	State  *appstate.State
}

// Config is a struct that holds the configuration for the application
type Config struct {
	DrainConditions DrainConditions
	KubeConfig      *rest.Config
	NodeName        string
}

func ReadConfiguration(ctx context.Context) (Config, error) {
	// grab the logger from the context
	vals := ctx.Value("values").(ContextValues)
	log := vals.Logger

	log.Debugw("Generating app config")

	config := viper.New()

	// set defaults for the config
	config.SetDefault("DRAIN_ON_FREEZE", false)
	config.SetDefault("DRAIN_ON_REBOOT", false)
	config.SetDefault("DRAIN_ON_REDEPLOY", true)
	config.SetDefault("DRAIN_ON_PREEMPT", true)
	config.SetDefault("DRAIN_ON_TERMINATE", true)

	// set viper to watch for a mounted config file and read it in, handling the error gracefully if it's missing
	config.SetConfigName("mechanic")
	config.AddConfigPath("/etc/mechanic")
	config.SetConfigType("yaml")
	if err := config.ReadInConfig(); err != nil {
		log.Warnw("Failed to read in config file, proceeding with default values and environment variables", "error", err)
	}

	config.SetEnvPrefix("MECHANIC")
	config.BindEnv("NODE_NAME")

	kc, err := rest.InClusterConfig()
	if err != nil {
		log.Errorw("Failed to get in cluster config", "error", err)
		return Config{}, err
	}

	// build our config for handling different drain conditions
	drainConfig := buildDrainConditions(config)

	log.Debugw("Successfully read configuration", "config", config.AllSettings())

	return Config{
		DrainConditions: drainConfig,
		KubeConfig:      kc,
		NodeName:        config.Get("NODE_NAME").(string),
	}, nil
}

// buildDrainConditions is a helper function that builds the DrainConditions struct from the mechanic config map in the cluster.
// if no config is found, it will return a struct with default values that match the behavior indicated at
// https://learn.microsoft.com/en-us/azure/aks/node-auto-repair#node-auto-drain
func buildDrainConditions(config *viper.Viper) DrainConditions {
	return DrainConditions{
		DrainOnFreeze:    config.GetBool("DRAIN_ON_FREEZE"),
		DrainOnReboot:    config.GetBool("DRAIN_ON_REBOOT"),
		DrainOnRedeploy:  config.GetBool("DRAIN_ON_REDEPLOY"),
		DrainOnPreempt:   config.GetBool("DRAIN_ON_PREEMPT"),
		DrainOnTerminate: config.GetBool("DRAIN_ON_TERMINATE"),
	}
}

func (dc *DrainConditions) DrainableConditions() []string {
	drainableConditions := []string{}

	if dc.DrainOnFreeze {
		drainableConditions = append(drainableConditions, "Freeze")
	}

	if dc.DrainOnReboot {
		drainableConditions = append(drainableConditions, "Reboot")
	}

	if dc.DrainOnRedeploy {
		drainableConditions = append(drainableConditions, "Redeploy")
	}

	if dc.DrainOnPreempt {
		drainableConditions = append(drainableConditions, "Preempt")
	}

	if dc.DrainOnTerminate {
		drainableConditions = append(drainableConditions, "Terminate")
	}

	return drainableConditions
}
