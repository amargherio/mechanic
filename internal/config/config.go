package config

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/amargherio/mechanic/internal/appstate"
	"github.com/amargherio/mechanic/pkg/consts"
	"github.com/fsnotify/fsnotify"
	"github.com/spf13/viper"
	"go.opentelemetry.io/otel/trace"
	"go.uber.org/zap"

	"k8s.io/client-go/rest"
)

const ENVVAR_PREFIX = "MECHANIC_"
const ENVVAR_POLLING_INTERVAL = 10 * time.Second

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
	PollingInterval            int  `mapstructure:"pollingInterval"`
}

// MechanicConfig represents the full configuration structure from mechanic.yaml
type MechanicConfig struct {
	ScheduledEvents           ScheduledEventDrainConditions `mapstructure:"scheduledEvents"`
	Optional                  OptionalDrainConditions       `mapstructure:"optionalConditions"`
	RuntimeEnv                string                        `mapstructure:"runtimeEnv"`
	EnableTracing             bool                          `mapstructure:"enableTracing"`
	BypassNodeProblemDetector bool                          `mapstructure:"bypassNodeProblemDetector"`
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
	BypassNodeProblemDetector     bool
}

// ReadConfiguration loads configuration from file and env vars and returns the *Config plus the underlying viper instance
// so callers can enable hot reloading.
func ReadConfiguration(ctx context.Context) (*Config, *viper.Viper, error) {
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
			PollingInterval:            30,
		},
		RuntimeEnv:                "prod",
		EnableTracing:             true,
		BypassNodeProblemDetector: false,
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
		return nil, nil, err
	}

	// PollingInterval is expected to be in seconds. Enforce a minimum of 1 second.
	if mechanicConfig.Optional.PollingInterval < 1 {
		log.Warnw("Optional polling interval is less than 1 second, resetting to minimum value of 1 second", "providedIntervalSeconds", mechanicConfig.Optional.PollingInterval)
		mechanicConfig.Optional.PollingInterval = 1
	}

	log.Debugw("Successfully read configuration", "config", mechanicConfig)

	return &Config{
		ScheduledEventDrainConditions: mechanicConfig.ScheduledEvents,
		OptionalDrainConditions:       mechanicConfig.Optional,
		KubeConfig:                    kc,
		NodeName:                      config.GetString("NODE_NAME"),
		EnableTracing:                 mechanicConfig.EnableTracing,
		RuntimeEnv:                    mechanicConfig.RuntimeEnv,
		BypassNodeProblemDetector:     mechanicConfig.BypassNodeProblemDetector,
	}, config, nil
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

// EnableHotReload sets up watchers on the configuration file and periodically checks for environment variable changes.
// When changes are detected the provided *Config object is updated in-place so existing references see new values.
func EnableHotReload(ctx context.Context, v *viper.Viper, cfg *Config, log *zap.SugaredLogger) {
	// helper to (re)load configuration and apply to existing cfg struct
	reload := func(trigger string) {
		log.Infow("Reloading configuration", "trigger", trigger)

		// Re-read the config file if present (viper caches env automatically)
		if err := v.ReadInConfig(); err != nil {
			log.Warnw("Failed to re-read config file during reload", "error", err)
		}

		var mc MechanicConfig
		if err := v.Unmarshal(&mc); err != nil {
			log.Errorw("Failed to unmarshal config during reload", "error", err)
			return
		}

		if mc.Optional.PollingInterval < 1 {
			mc.Optional.PollingInterval = 1
		}

		// Update fields in place
		cfg.ScheduledEventDrainConditions = mc.ScheduledEvents
		cfg.OptionalDrainConditions = mc.Optional
		cfg.RuntimeEnv = mc.RuntimeEnv
		cfg.EnableTracing = mc.EnableTracing
		cfg.BypassNodeProblemDetector = mc.BypassNodeProblemDetector
		cfg.NodeName = v.GetString("NODE_NAME")

		log.Infow("Configuration reloaded", "runtimeEnv", cfg.RuntimeEnv, "nodeName", cfg.NodeName, "bypassNodeProblemDetector", cfg.BypassNodeProblemDetector)
	}

	// Watch the config file for changes
	configFile := v.ConfigFileUsed()
	if configFile == "" {
		log.Warnw("No config file specified; hot reload via file watcher will not be enabled")
	} else {
		if _, err := os.Stat(configFile); err != nil {
			log.Warnw("Config file not found or inaccessible; hot reload via file watcher may not work", "configFile", configFile, "error", err)
		}

		v.WatchConfig()
		v.OnConfigChange(func(e fsnotify.Event) {
			defer func() {
				if r := recover(); r != nil {
					log.Errorw("Panic occurred during config file watcher callback", "recover", r)
				}
			}()
			reload("file-change")
		})
	}

	// Environment variable polling (Kubernetes cannot mutate env in-place but useful for local dev or injected updates)
	prevHash := hashMechanicEnvs()
	go func() {
		ticker := time.NewTicker(ENVVAR_POLLING_INTERVAL)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				h := hashMechanicEnvs()
				if h != prevHash {
					prevHash = h
					reload("env-change")
				}
			}
		}
	}()
}

// hashMechanicEnvs returns a stable hash of current MECHANIC_* environment variables.
func hashMechanicEnvs() string {
	envs := os.Environ()
	var filtered []string
	for _, e := range envs {
		if strings.HasPrefix(e, ENVVAR_PREFIX) {
			filtered = append(filtered, e)
		}
	}
	sort.Strings(filtered)
	sum := sha256.Sum256([]byte(strings.Join(filtered, "|")))
	return hex.EncodeToString(sum[:])
}
