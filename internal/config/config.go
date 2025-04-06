package config

import (
	"context"

	"github.com/amargherio/mechanic/internal/appstate"
	"github.com/amargherio/mechanic/pkg/consts"
	"github.com/spf13/viper"
	"go.opentelemetry.io/otel/trace"
	"go.uber.org/zap"

	"k8s.io/client-go/rest"
)

// ScheduledEventDrainConditions defines which VM scheduled events should trigger node draining
type ScheduledEventDrainConditions struct {
	Freeze        bool `mapstructure:"freeze"`
	Reboot        bool `mapstructure:"reboot"`
	Redeploy      bool `mapstructure:"redeploy"`
	Preempt       bool `mapstructure:"preempt"`
	Terminate     bool `mapstructure:"terminate"`
	LiveMigration bool `mapstructure:"liveMigration"`
}

// OptionalDrainConditions defines additional node conditions that should trigger node draining
type OptionalDrainConditions struct {
	KubeletProblem             bool `mapstructure:"kubeletProblem"`
	KernelDeadlock             bool `mapstructure:"kernelDeadlock"`
	FrequentKubeletRestarts    bool `mapstructure:"frequentKubeletRestarts"`
	FrequentContainerdRestarts bool `mapstructure:"frequentContainerdRestarts"`
	FsCorrupt                  bool `mapstructure:"fsCorrupt"`
}

// MechanicConfig represents the full configuration structure from mechanic.yaml
type MechanicConfig struct {
	ScheduledEvents ScheduledEventDrainConditions `mapstructure:"scheduledEvents"`
	Optional        OptionalDrainConditions       `mapstructure:"optionalConditions"`
	RuntimeEnv      string                        `mapstructure:"runtimeEnv"`
	EnableTracing   bool                          `mapstructure:"enableTracing"`
}

// ContextValues is a struct that holds the logger and state of the application for use in the shared application context
type ContextValues struct {
	Logger *zap.SugaredLogger
	State  *appstate.State
	Tracer *trace.Tracer
}

// Config is a struct that holds the configuration for the application
type Config struct {
	RuntimeEnv                    string
	ScheduledEventDrainConditions ScheduledEventDrainConditions
	OptionalDrainConditions       OptionalDrainConditions
	KubeConfig                    *rest.Config
	NodeName                      string
	EnableTracing                 bool
}

func ReadConfiguration(ctx context.Context) (Config, error) {
	vals := ctx.Value("values").(*ContextValues)
	log := vals.Logger

	log.Debugw("Generating app config")

	config := viper.New()

	// Set defaults using a default MechanicConfig
	defaultConfig := MechanicConfig{
		ScheduledEvents: ScheduledEventDrainConditions{
			Freeze:        false,
			Reboot:        false,
			Redeploy:      true,
			Preempt:       true,
			Terminate:     true,
			LiveMigration: true,
		},
		Optional: OptionalDrainConditions{
			KubeletProblem:             false,
			KernelDeadlock:             false,
			FrequentKubeletRestarts:    false,
			FrequentContainerdRestarts: false,
			FsCorrupt:                  false,
		},
		RuntimeEnv:    "prod",
		EnableTracing: true,
	}

	// Set up Viper to find and read the config file
	config.SetConfigName("mechanic")
	config.AddConfigPath("/etc/mechanic")
	config.SetConfigType("yaml")

	// Read the config file, handling errors gracefully
	if err := config.ReadInConfig(); err != nil {
		log.Warnw("Failed to read in config file, proceeding with default values and environment variables", "error", err)
	}

	// Allow environment variable overrides
	config.SetEnvPrefix("MECHANIC")
	config.AutomaticEnv()
	config.BindEnv("NODE_NAME")

	// Create a mechanic config instance and unmarshal configuration into it
	mechanicConfig := defaultConfig
	if err := config.Unmarshal(&mechanicConfig); err != nil {
		log.Warnw("Failed to unmarshal config, using default values", "error", err)
	}

	// Get Kubernetes configuration
	kc, err := rest.InClusterConfig()
	if err != nil {
		log.Errorw("Failed to get in cluster config", "error", err)
		return Config{}, err
	}

	log.Debugw("Successfully read configuration", "config", mechanicConfig)

	return Config{
		ScheduledEventDrainConditions: mechanicConfig.ScheduledEvents,
		OptionalDrainConditions:       mechanicConfig.Optional,
		KubeConfig:                    kc,
		NodeName:                      config.GetString("NODE_NAME"),
		EnableTracing:                 mechanicConfig.EnableTracing,
		RuntimeEnv:                    mechanicConfig.RuntimeEnv,
	}, nil
}

// DrainableConditions returns a list of VM event conditions that would trigger a drain
func (dc *ScheduledEventDrainConditions) DrainableConditions() []string {
	drainableConditions := []string{}

	if dc.Freeze || dc.LiveMigration {
		drainableConditions = append(drainableConditions, string(consts.Freeze))
	}

	if dc.Reboot {
		drainableConditions = append(drainableConditions, string(consts.Reboot))
	}

	if dc.Redeploy {
		drainableConditions = append(drainableConditions, string(consts.Redeploy))
	}

	if dc.Preempt {
		drainableConditions = append(drainableConditions, string(consts.Preempt))
	}

	if dc.Terminate {
		drainableConditions = append(drainableConditions, string(consts.Terminate))
	}

	return drainableConditions
}

// OptionalDrainableConditions returns a list of optional node conditions that would trigger a drain
func (oc *OptionalDrainConditions) OptionalDrainableConditions() []string {
	drainableConditions := []string{}

	if oc.KubeletProblem {
		drainableConditions = append(drainableConditions, string(consts.KubeletProblem))
	}

	if oc.KernelDeadlock {
		drainableConditions = append(drainableConditions, string(consts.KernelDeadlock))
	}

	if oc.FrequentKubeletRestarts {
		drainableConditions = append(drainableConditions, string(consts.FrequentKubeletRestart))
	}

	if oc.FrequentContainerdRestarts {
		drainableConditions = append(drainableConditions, string(consts.FrequentContainerdRestart))
	}

	if oc.FsCorrupt {
		drainableConditions = append(drainableConditions, string(consts.FileSystemCorruptionProblem))
	}

	return drainableConditions
}
