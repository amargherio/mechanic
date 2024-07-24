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
	log := ctx.Value("logger").(*zap.SugaredLogger)
	log.Debugw("Generating app config")

	config := viper.New()
	config.SetEnvPrefix("MECHANIC")
	config.BindEnv("NODE_NAME")

	kc, err := rest.InClusterConfig()
	if err != nil {
		log.Errorw("Failed to get in cluster config", "error", err)
		return Config{}, err
	}

	log.Debugw("Successfully read configuration", "config", config.AllSettings())

	return Config{
		DrainConditions: DrainConditions{},
		KubeConfig:      kc,
		NodeName:        config.Get("NODE_NAME").(string),
	}, nil
}
