package single

import (
	"context"
	"embed"
	"fmt"
	"strings"

	// datacatalogConfig "github.com/flyteorg/datacatalog/pkg/config"
	// "github.com/flyteorg/datacatalog/pkg/rpc/datacatalogservice"
	"github.com/flyteorg/flyteadmin/pkg/clusterresource"
	_ "github.com/flyteorg/flyteadmin/pkg/common"
	"github.com/flyteorg/flyteadmin/pkg/runtime"
	adminServer "github.com/flyteorg/flyteadmin/pkg/server"
	adminScheduler "github.com/flyteorg/flyteadmin/scheduler"
	_ "github.com/flyteorg/flyteplugins/go/tasks/plugins/array/k8s"
	_ "github.com/flyteorg/flyteplugins/go/tasks/plugins/k8s/pod"
	propellerEntrypoint "github.com/flyteorg/flytepropeller/pkg/controller"
	propellerConfig "github.com/flyteorg/flytepropeller/pkg/controller/config"
	_ "github.com/flyteorg/flytestdlib/contextutils"
	"github.com/flyteorg/flytestdlib/logger"
	"github.com/flyteorg/flytestdlib/promutils"
	_ "github.com/flyteorg/flytestdlib/promutils/labeled"
	_ "github.com/golang/glog"
	"github.com/spf13/cobra"
	"golang.org/x/sync/errgroup"
	_ "gorm.io/driver/postgres" // Required to import database driver.
	"net/http"
)

var console embed.FS

func WriteIndex(writer http.ResponseWriter) {
	b, err := console.ReadFile("dist/index.html")
	if err != nil {
		http.Error(writer, err.Error(), http.StatusInternalServerError)
		return
	}
	_, err = writer.Write(b)
	if err != nil {
		http.Error(writer, err.Error(), http.StatusInternalServerError)
	}
}

func GetConsoleHandlers() map[string]func(http.ResponseWriter, *http.Request) {
	handlers := make(map[string]func(http.ResponseWriter, *http.Request))
	// Serves console
	rawFS := http.FileServer(http.FS(console))
	consoleFS := http.StripPrefix("/console/", rawFS)
	handlers["/console/assets/"] = func(writer http.ResponseWriter, request *http.Request) {
		fmt.Printf("all files under console returning, %s\n", request.URL.Path)
		consoleFS.ServeHTTP(writer, request)
	}

	handlers["/console/"] = func(writer http.ResponseWriter, request *http.Request) {
		newPath := strings.TrimLeft(request.URL.Path, "/console")
		if strings.Contains(newPath, "/") {
			logger.Infof(context.TODO(), "Redirecting request to index.html, %s", request.URL.Path)
			WriteIndex(writer)
		} else {
			consoleFS.ServeHTTP(writer, request)
		}
	}

	handlers["/console"] = func(writer http.ResponseWriter, request *http.Request) {
		logger.Infof(context.TODO(), "Returning index.html")
		WriteIndex(writer)
	}

	return handlers
}

func startClusterResourceController(ctx context.Context) error {
	configuration := runtime.NewConfigurationProvider()
	scope := promutils.NewScope(configuration.ApplicationConfiguration().GetTopLevelConfig().MetricsScope).NewSubScope("clusterresource")
	clusterResourceController, err := clusterresource.NewClusterResourceControllerFromConfig(ctx, scope, configuration)
	if err != nil {
		return err
	}
	clusterResourceController.Run()
	logger.Infof(ctx, "ClusterResourceController started running successfully")
	return nil
}

func startAdmin(ctx context.Context) error {
	logger.Infof(ctx, "Running Database Migrations...")
	if err := adminServer.Migrate(ctx); err != nil {
		return err
	}
	logger.Infof(ctx, "Seeding default projects...")
	if err := adminServer.SeedProjects(ctx, []string{"flytesnacks"}); err != nil {
		return err
	}
	g := new(errgroup.Group)
	g.Go(func() error {
		logger.Infof(ctx, "Starting Scheduler...")
		return adminScheduler.StartScheduler(ctx)
	})
	//logger.Infof(ctx, "Starting cluster resource controller...")
	//g.Go(func() error {
	//	return startClusterResourceController(ctx)
	//})
	g.Go(func() error {
		logger.Infof(ctx, "Starting Admin server...")
		return adminServer.Serve(ctx, GetConsoleHandlers())
	})
	return g.Wait()
}

func startPropeller(ctx context.Context) error {
	return propellerEntrypoint.StartController(ctx, propellerConfig.GetConfig(), "all")
}

//func startDataCatalog(ctx context.Context) error {
//	return datacatalogservice.ServeInsecure(ctx, datacatalogConfig.GetConfig())
//}

var startCmd = &cobra.Command{
	Use:   "start",
	Short: "This command will start the flyte native scheduler and periodically get new schedules from the db for scheduling",
	RunE: func(cmd *cobra.Command, args []string) error {
		ctx := context.Background()
		g, childCtx := errgroup.WithContext(ctx)
		g.Go(func() error {
			err := startAdmin(childCtx)
			if err != nil {
				logger.Errorf(childCtx, "Failed to start Admin, err: %v", err)
				return err
			}
			return nil
		})

		g.Go(func() error {
			err := startPropeller(childCtx)
			if err != nil {
				logger.Errorf(childCtx, "Failed to start Propeller, err: %v", err)
				return err
			}
			return nil
		})
		//
		//g.Go(func() error {
		//	err := startDataCatalog(childCtx)
		//	if err != nil {
		//		logger.Errorf(childCtx, "Failed to start Datacatalog, err: %v", err)
		//		return err
		//	}
		//	return nil
		//})

		return g.Wait()
	},
}

func init() {
	RootCmd.AddCommand(startCmd)
	// Set Keys
	//labeled.SetMetricKeys(contextutils.AppNameKey, contextutils.ProjectKey, contextutils.DomainKey,
	//	contextutils.ExecIDKey, contextutils.WorkflowIDKey, contextutils.NodeIDKey, contextutils.TaskIDKey,
	//	contextutils.TaskTypeKey, common.RuntimeTypeKey, common.RuntimeVersionKey)
}
