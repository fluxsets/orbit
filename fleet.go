package fleet

import (
	"context"
	"github.com/fluxsets/fleet/option"
	"github.com/oklog/run"
	"gocloud.dev/server/health"
	"log"
	"log/slog"
	"os"
	"os/signal"
	"strings"
	"syscall"
)

type Fleet interface {
	Init() error
	Close()
	Config() Configurer
	Option() *option.Option
	Context() context.Context
	// MainCommand 注册启动的命令，用于 CLI 模式
	MainCommand(cmd CommandFunc) error
	// Load 加载组件，但只有当应用启动后才会执行 Start
	Load(components ...Component) error
	LoadFromProducer(producers ...ComponentProducer) error
	HealthCheck() HealthCheckFunc
	Run() error
	Hooks() Hooks
	SetLogger(logger *slog.Logger)
	Logger(args ...any) *slog.Logger
}

type fleet struct {
	ctx            context.Context
	cancelCtx      context.CancelFunc
	o              option.Option
	runG           *run.Group
	hooks          *hooks
	logger         *slog.Logger
	c              Configurer
	healthCheckers []health.Checker
}

func (ft *fleet) SetLogger(logger *slog.Logger) {
	slog.SetDefault(logger)
	ft.logger = logger
}

func (ft *fleet) HealthCheck() HealthCheckFunc {
	return func() []health.Checker {
		return ft.healthCheckers
	}
}

func (ft *fleet) MainCommand(cmd CommandFunc) error {
	return ft.Load(NewCommand(cmd))
}

func (ft *fleet) Close() {
	ft.cancelCtx()
}

func (ft *fleet) Init() error {
	ft.initConfigurer()
	ft.initLogger()
	return nil
}

func (ft *fleet) initLogger() {
	ft.logger = slog.Default()
}

func (ft *fleet) initConfigurer() {
	configDir := ft.o.ConfigDir
	config := ft.o.Config
	configType := "yaml"
	if ft.o.ConfigType != "" {
		configType = ft.o.ConfigType
	}
	if configDir != "" {

		configDirs := strings.Split(configDir, ",")
		ft.c = newConfigFromDir(configDirs, configType)
	} else if config != "" {
		ft.c = newConfigFromFile(config)
	} else {
		ft.c = newConfigFromDir([]string{"./configs"}, configType)
	}

	ft.c.Merge(ft.o.PropertiesAsMap())
}

func (ft *fleet) LoadFromProducer(producers ...ComponentProducer) error {
	for _, producer := range producers {
		produce := producer.Component
		options := producer.Option()
		options.ensureDefaults()
		var components []Component
		for i := 0; i < options.Instances; i++ {
			comp := produce()
			components = append(components, comp)
		}
		if err := ft.Load(components...); err != nil {
			return err
		}
	}
	return nil
}

func (ft *fleet) Config() Configurer {
	return ft.c
}

func (ft *fleet) Logger(args ...any) *slog.Logger {
	return ft.logger.With(args...)
}

func (ft *fleet) Context() context.Context {
	return ft.ctx
}

func (ft *fleet) Option() *option.Option {
	return &ft.o
}

func (ft *fleet) Load(components ...Component) error {
	for _, comp := range components {
		ctx, cancel := context.WithCancel(context.Background())
		if err := comp.Init(ft); err != nil {
			cancel()
			return err
		}
		ft.runG.Add(func() error {
			return comp.Start(ctx)
		}, func(err error) {
			comp.Stop(ctx)
			cancel()
		})
		if hc, ok := comp.(health.Checker); ok {
			ft.healthCheckers = append(ft.healthCheckers, hc)
		}
	}
	return nil
}

func (ft *fleet) Run() error {
	ft.Logger().Info("starting")
	ft.runG.Add(func() error {
		ft.Logger().Info("calling on start hooks")
		for _, fn := range ft.hooks.onStarts {
			if err := fn(ft.ctx); err != nil {
				return err
			}
		}
		select {
		case <-ft.ctx.Done():
			return nil
		}
	}, func(err error) {
		ft.Close()
	})

	ft.runG.Add(func() error {
		exit := make(chan os.Signal, 1)
		signal.Notify(exit, syscall.SIGINT, syscall.SIGTERM)
		select {
		case <-ft.ctx.Done():
			return nil
		case <-exit:
			return nil
		}
	}, func(err error) {
		ft.Logger().Info("shutting down")
		ctx, cancelCtx := context.WithTimeout(context.TODO(), ft.o.ShutdownTimeout)
		defer cancelCtx()
		ft.Logger().Info("calling on stop hooks")
		for _, fn := range ft.hooks.onStops {
			fn := fn
			if err := fn(ctx); err != nil {
				ft.logger.ErrorContext(ft.ctx, "post stop func called error", "error", err)
			}
		}
	})
	return ft.runG.Run()
}

func (ft *fleet) Hooks() Hooks {
	return ft.hooks
}

func newFleet(o option.Option) Fleet {
	o.EnsureDefaults()
	ft := &fleet{
		o:    o,
		runG: &run.Group{},
		hooks: &hooks{
			onStarts: []HookFunc{},
			onStops:  []HookFunc{},
		},
	}
	ft.ctx, ft.cancelCtx = context.WithCancel(context.Background())
	if err := ft.Init(); err != nil {
		log.Fatal(err)
	}
	return ft
}
