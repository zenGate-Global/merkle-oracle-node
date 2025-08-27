package api

import (
	"errors"
	"fmt"
	"net/http"
	"net/http/pprof"
	"time"

	"zenGate-Global/merkle-oracle-node/internal/config"
	"zenGate-Global/merkle-oracle-node/internal/database"
	"zenGate-Global/merkle-oracle-node/internal/logging"

	scalargo "github.com/bdpiprava/scalar-go"
	ginzap "github.com/gin-contrib/zap"
	"github.com/gin-gonic/gin"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// @title        Merkle Oracle Node API
// @version      1.0.0
// @license.name Apache 2.0
// @license.url  http://www.apache.org/licenses/LICENSE-2.0.html
// @host         localhost:8080
// @BasePath     /
// @tag.name     objects
// @tag.description Object Queries
// @tag.name     system
// @tag.description System status and health monitoring endpoints
// @schemes      http https

var scalarHTML []byte
var scalarHTMLGenerationErr error

func generateScalarDocs() {

	specDir := "./docs"

	baseFileName := "swagger.json"

	htmlContent, err := scalargo.NewV2(
		scalargo.WithSpecDir(specDir),
		scalargo.WithBaseFileName(baseFileName),
		scalargo.WithTheme(scalargo.ThemeBluePlanet),
		scalargo.WithMetaDataOpts(
			scalargo.WithTitle("Merkle Oracle Node API"),
		),
		scalargo.WithLayout(scalargo.LayoutClassic),
	)

	if err != nil {
		scalarHTMLGenerationErr = fmt.Errorf(
			"failed to generate Scalar documentation: %w",
			err,
		)
		logging.GetLogger().
			Error("Failed to generate Scalar documentation", "error", scalarHTMLGenerationErr)
		scalarHTML = nil
		return
	}
	scalarHTML = []byte(htmlContent)
	scalarHTMLGenerationErr = nil
	logging.GetLogger().Info("Scalar API documentation generated successfully.")
}

var ErrInvalidRequest = errors.New("invalid request")

func Start(
	cfg *config.Config,
	db *database.Database,
) (*http.Server, error) {
	gin.SetMode(gin.ReleaseMode)
	router := gin.New()
	router.Use(gin.Recovery())

	router.Use(func(c *gin.Context) {
		c.Set("db", db)
		c.Set("cfg", cfg)
		c.Next()
	})

	logger := logging.GetLogger()
	router.Use(ginzap.GinzapWithConfig(logger.Desugar(), &ginzap.Config{
		TimeFormat: time.RFC3339,
		UTC:        true,
	}))
	router.Use(ginzap.RecoveryWithZap(logger.Desugar(), true))

	// Health check endpoint
	router.GET("/healthcheck", handleHealthcheck)

	// Object endpoint
	router.GET("/objects/:id", handleGetObjectByID)

	// Setup metrics endpoint
	router.GET("/metrics", gin.WrapH(promhttp.Handler()))

	if cfg.Server.EnableDebug {
		debugGroup := router.Group("/debug/pprof")
		{
			debugGroup.GET("/", gin.WrapF(pprof.Index))
			debugGroup.GET("/cmdline", gin.WrapF(pprof.Cmdline))
			debugGroup.GET("/profile", gin.WrapF(pprof.Profile))
			debugGroup.POST("/symbol", gin.WrapF(pprof.Symbol))
			debugGroup.GET("/symbol", gin.WrapF(pprof.Symbol))
			debugGroup.GET("/trace", gin.WrapF(pprof.Trace))
			debugGroup.GET("/allocs", gin.WrapH(pprof.Handler("allocs")))
			debugGroup.GET("/block", gin.WrapH(pprof.Handler("block")))
			debugGroup.GET("/goroutine", gin.WrapH(pprof.Handler("goroutine")))
			debugGroup.GET("/heap", gin.WrapH(pprof.Handler("heap")))
			debugGroup.GET("/mutex", gin.WrapH(pprof.Handler("mutex")))
			debugGroup.GET(
				"/threadcreate",
				gin.WrapH(pprof.Handler("threadcreate")),
			)
		}
	}

	// Generate and setup API docs
	generateScalarDocs()
	router.GET("/docs", func(c *gin.Context) {
		if scalarHTMLGenerationErr != nil {
			logger.Error(
				"API documentation unavailable",
				"error",
				scalarHTMLGenerationErr,
			)
			c.String(
				http.StatusNotFound,
				"API documentation is currently unavailable.",
			)
			return
		}
		if scalarHTML == nil {
			c.String(
				http.StatusNotFound,
				"API documentation is currently unavailable (not generated).",
			)
			return
		}
		c.Data(http.StatusOK, "text/html; charset=utf-8", scalarHTML)
	})

	serverAddr := fmt.Sprintf("%s:%d",
		cfg.Server.ListenAddress,
		cfg.Server.ListenPort,
	)

	logger.Info("Starting API server", "address", serverAddr)
	logger.Info("Metrics available at /metrics")
	if cfg.Server.EnableDebug {
		logger.Info("Debug endpoints available at /debug/pprof/*")
	}

	server := &http.Server{
		Addr:    serverAddr,
		Handler: router,
	}

	go func() {
		if err := server.ListenAndServe(); err != nil &&
			err != http.ErrServerClosed {
			logger.Error("API server failed", "error", err)
		}
	}()

	return server, nil
}

// handleHealthcheck godoc
// @Summary      Health Check
// @Description  Returns 200 if the service is alive.
// @Tags         system
// @Produce      json
// @Success      200  {object}  map[string]bool
// @Router       /healthcheck [get]
func handleHealthcheck(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{"ok": true})
}

// handleGetObjectByID godoc
// @Summary      Get Object by ID
// @Description  Retrieves an object by its ID from the database with key-value pairs. Optionally specify a timestamp to get historical state.
// @Tags         objects
// @Accept       json
// @Produce      json
// @Param        id          path      string  true   "Object ID"
// @Param        timestamp   query     string  false  "RFC3339 timestamp to get object state at specific time (e.g. 2024-01-15T10:30:00Z). If not provided, returns current state."
// @Success      200  {object}  map[string]interface{} "Object data with timestamp"
// @Failure      400  {object}  map[string]string "Invalid ID format or timestamp"
// @Failure      404  {object}  map[string]string "Object not found"
// @Failure      500  {object}  map[string]string "Internal server error"
// @Router       /objects/{id} [get]
func handleGetObjectByID(c *gin.Context) {
	db := c.MustGet("db").(*database.Database)
	id := c.Param("id")

	// Parse optional timestamp parameter
	timestampParam := c.Query("timestamp")
	var targetTimestamp *time.Time
	if timestampParam != "" {
		parsed, err := time.Parse(time.RFC3339, timestampParam)
		if err != nil {
			BadRequest(c, fmt.Errorf("invalid timestamp format, expected RFC3339 (e.g. 2024-01-15T10:30:00Z): %w", err))
			return
		}
		targetTimestamp = &parsed
	}

	obj, err := db.GetObjectByID(c.Request.Context(), id)
	if err != nil {
		ServerError(c, fmt.Errorf("failed to get object: %w", err))
		return
	}

	if obj == nil {
		NotFound(c, "object not found")
		return
	}

	var objectValues map[string]any
	var responseTimestamp time.Time

	if targetTimestamp != nil {
		objectValues, err = db.GetObjectValuesAtTimestamp(c.Request.Context(), id, *targetTimestamp)
		responseTimestamp = *targetTimestamp
	} else {
		objectValues, err = db.GetObjectCurrentValues(c.Request.Context(), id)
		responseTimestamp = time.Now().UTC()
	}

	if err != nil {
		ServerError(c, fmt.Errorf("failed to get object values: %w", err))
		return
	}

	var objectData interface{}
	if len(objectValues) == 0 {
		objectData = nil
	} else {
		objectData = objectValues
	}

	response := map[string]interface{}{
		"timestamp": responseTimestamp.Format(time.RFC3339Nano),
		"object":    objectData,
	}

	c.JSON(http.StatusOK, response)
}

func ServerError(c *gin.Context, err error) {
	logging.GetLogger().
		Error("server error", "error", err, "path", c.Request.URL.Path)
	c.JSON(
		http.StatusInternalServerError,
		gin.H{"error": "internal server error"},
	)
}

func NotFound(c *gin.Context, message string) {
	c.JSON(http.StatusNotFound, gin.H{"error": message})
}

func BadRequest(c *gin.Context, err error) {
	c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
}
