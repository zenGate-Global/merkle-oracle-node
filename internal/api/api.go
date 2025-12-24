package api

import (
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"zenGate-Global/merkle-oracle-node/internal/config"
	"zenGate-Global/merkle-oracle-node/internal/database"
	"zenGate-Global/merkle-oracle-node/internal/logging"

	scalargo "github.com/bdpiprava/scalar-go"
	ginzap "github.com/gin-contrib/zap"
	"github.com/gin-gonic/gin"
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
// @tag.name     statistics
// @tag.description Statistics and analytics endpoints
// @schemes      http https

var scalarHTML []byte
var scalarHTMLGenerationErr error

func generateScalarDocs() {

	specURL := "/docs/swagger.json"

	htmlContent, err := scalargo.NewV2(
		scalargo.WithSpecURL(specURL),
		scalargo.WithTheme(scalargo.ThemeBluePlanet),
		scalargo.WithMetaDataOpts(
			scalargo.WithTitle("Merkle Oracle Node API"),
		),
		scalargo.WithLayout(scalargo.LayoutClassic),
		scalargo.WithServers(
			scalargo.Server{
				URL:         "https://merkle-staging4.zengate-dev.com",
				Description: "Staging Server",
			},
		),
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

	// Object endpoints
	router.GET("/objects", handleGetAllObjectIDs)
	router.GET("/objects/:id", handleGetObjectByID)
	router.GET("/objects/active", handleGetActiveObjectIDs)
	router.GET("/objects/deleted", handleGetDeletedObjectIDs)
	router.GET("/objects/:id/keys/:key", handleGetValueByKey)

	// Key endpoints
	router.GET("/keys/:keyHash", handleGetValueByKeyHash)

	// Statistics endpoints
	router.GET("/statistics/costs", handleGetCostStatistics)

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

	// Serve swagger.json for API documentation
	router.GET("/docs/swagger.json", func(c *gin.Context) {
		c.File("./docs/swagger.json")
	})

	serverAddr := fmt.Sprintf("%s:%d",
		cfg.Server.ListenAddress,
		cfg.Server.ListenPort,
	)

	logger.Infof("Starting API server on %s", serverAddr)

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
// @Description  Retrieves an object by its ID from the database with key-value pairs. Optionally specify a timestamp or slot to get historical state.
// @Tags         objects
// @Accept       json
// @Produce      json
// @Param        id          path      string  true   "Object ID"
// @Param        timestamp   query     string  false  "RFC3339 timestamp to get object state at specific time (e.g. 2024-01-15T10:30:00Z). Cannot be used with slot parameter."
// @Param        slot        query     int     false  "Slot number to get object state at specific slot. Cannot be used with timestamp parameter."
// @Success      200  {object}  map[string]interface{} "Object data with timestamp and slot"
// @Failure      400  {object}  map[string]string "Invalid ID format, timestamp, slot, or conflicting parameters"
// @Failure      404  {object}  map[string]string "Object not found"
// @Failure      500  {object}  map[string]string "Internal server error"
// @Router       /objects/{id} [get]
func handleGetObjectByID(c *gin.Context) {
	db := c.MustGet("db").(*database.Database)
	id := c.Param("id")

	timestampParam := c.Query("timestamp")
	slotParam := c.Query("slot")

	if timestampParam != "" && slotParam != "" {
		BadRequest(
			c,
			fmt.Errorf("cannot specify both timestamp and slot parameters"),
		)
		return
	}

	var targetTimestamp *time.Time
	var targetSlot *int64

	if timestampParam != "" {
		parsed, err := parseRFC3339Flexible(timestampParam)
		if err != nil {
			BadRequest(
				c,
				fmt.Errorf(
					"invalid timestamp format (RFC3339/RFC3339Nano): %w",
					err,
				),
			)
			return
		}
		targetTimestamp = &parsed
	}

	if slotParam != "" {
		parsed, err := strconv.ParseInt(slotParam, 10, 64)
		if err != nil {
			BadRequest(
				c,
				fmt.Errorf("invalid slot parameter: must be a number"),
			)
			return
		}
		if parsed <= 0 {
			BadRequest(
				c,
				fmt.Errorf("invalid slot parameter: must be greater than 0"),
			)
			return
		}
		targetSlot = &parsed
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

	var objectData interface{}
	var responseTimestamp time.Time
	var responseSlot int64

	if targetTimestamp != nil {
		result, err := db.GetObjectValuesAtTimestampWithSlot(
			c.Request.Context(),
			id,
			*targetTimestamp,
		)
		if err != nil {
			ServerError(
				c,
				fmt.Errorf("failed to get object values at timestamp: %w", err),
			)
			return
		}
		responseTimestamp = result.Timestamp
		responseSlot = result.Slot
		if len(result.Values) == 0 {
			objectData = nil
		} else {
			objectData = result.Values
		}
	} else if targetSlot != nil {
		result, err := db.GetObjectValuesAtSlotWithTimestamp(
			c.Request.Context(),
			id,
			*targetSlot,
		)
		if err != nil {
			ServerError(c, fmt.Errorf("failed to get object values at slot: %w", err))
			return
		}
		responseTimestamp = result.Timestamp
		responseSlot = result.Slot
		if len(result.Values) == 0 {
			objectData = nil
		} else {
			objectData = result.Values
		}
	} else {
		result, err := db.GetObjectCurrentValuesWithTimestamp(c.Request.Context(), id)
		if err != nil {
			ServerError(c, fmt.Errorf("failed to get current object values: %w", err))
			return
		}
		responseTimestamp = result.Timestamp
		responseSlot = result.Slot
		if len(result.Values) == 0 {
			objectData = nil
		} else {
			objectData = result.Values
		}
	}

	response := map[string]interface{}{
		"timestamp": responseTimestamp.Format(time.RFC3339Nano),
		"slot":      responseSlot,
		"object":    objectData,
	}

	c.JSON(http.StatusOK, response)
}

// handleGetAllObjectIDs godoc
// @Summary      Get All Object IDs
// @Description  Retrieves a paginated list of all tracked object IDs from the database.
// @Tags         objects
// @Accept       json
// @Produce      json
// @Param        limit   query     int     false  "Number of items per page (default: 20, max: 1000)"
// @Param        offset  query     int     false  "Number of items to skip (default: 0)"
// @Success      200  {object}  database.GetAllObjectIDsResult "Paginated list of object IDs"
// @Failure      400  {object}  map[string]string "Invalid query parameters"
// @Failure      500  {object}  map[string]string "Internal server error"
// @Router       /objects [get]
func handleGetAllObjectIDs(c *gin.Context) {
	db := c.MustGet("db").(*database.Database)
	cfg := c.MustGet("cfg").(*config.Config)

	limit, offset, err := parsePageParams(c, cfg)
	if err != nil {
		BadRequest(c, err)
		return
	}

	result, err := db.GetAllObjectIDs(c.Request.Context(), limit, offset)
	if err != nil {
		ServerError(c, fmt.Errorf("failed to get object IDs: %w", err))
		return
	}

	c.JSON(http.StatusOK, result)
}

// handleGetActiveObjectIDs godoc
// @Summary      Get Active Object IDs
// @Description  Retrieves a paginated list of object IDs that have at least one non-deleted key from the database.
// @Tags         objects
// @Accept       json
// @Produce      json
// @Param        limit   query     int     false  "Number of items per page (default: 20, max: 1000)"
// @Param        offset  query     int     false  "Number of items to skip (default: 0)"
// @Success      200  {object}  database.GetAllObjectIDsResult "Paginated list of active object IDs"
// @Failure      400  {object}  map[string]string "Invalid query parameters"
// @Failure      500  {object}  map[string]string "Internal server error"
// @Router       /objects/active [get]
func handleGetActiveObjectIDs(c *gin.Context) {
	db := c.MustGet("db").(*database.Database)
	cfg := c.MustGet("cfg").(*config.Config)

	limit, offset, err := parsePageParams(c, cfg)
	if err != nil {
		BadRequest(c, err)
		return
	}

	result, err := db.GetActiveObjectIDs(c.Request.Context(), limit, offset)
	if err != nil {
		ServerError(c, fmt.Errorf("failed to get active object IDs: %w", err))
		return
	}

	c.JSON(http.StatusOK, result)
}

// handleGetDeletedObjectIDs godoc
// @Summary      Get Deleted Object IDs
// @Description  Retrieves a paginated list of object IDs that have no non-deleted keys from the database.
// @Tags         objects
// @Accept       json
// @Produce      json
// @Param        limit   query     int     false  "Number of items per page (default: 20, max: 1000)"
// @Param        offset  query     int     false  "Number of items to skip (default: 0)"
// @Success      200  {object}  database.GetAllObjectIDsResult "Paginated list of deleted object IDs"
// @Failure      400  {object}  map[string]string "Invalid query parameters"
// @Failure      500  {object}  map[string]string "Internal server error"
// @Router       /objects/deleted [get]
func handleGetDeletedObjectIDs(c *gin.Context) {
	db := c.MustGet("db").(*database.Database)
	cfg := c.MustGet("cfg").(*config.Config)

	limit, offset, err := parsePageParams(c, cfg)
	if err != nil {
		BadRequest(c, err)
		return
	}

	result, err := db.GetDeletedObjectIDs(c.Request.Context(), limit, offset)
	if err != nil {
		ServerError(c, fmt.Errorf("failed to get deleted object IDs: %w", err))
		return
	}

	c.JSON(http.StatusOK, result)
}

// handleGetValueByKey godoc
// @Summary      Get Value by Key
// @Description  Retrieves the current value for a specific object ID and key name, along with keyHash, timestamp and slot information.
// @Tags         objects
// @Accept       json
// @Produce      json
// @Param        id          path      string  true   "Object ID"
// @Param        key         path      string  true   "Key name"
// @Success      200  {object}  map[string]interface{} "Value with keyHash (hex), timestamp and slot"
// @Failure      404  {object}  map[string]string "Key not found"
// @Failure      500  {object}  map[string]string "Internal server error"
// @Router       /objects/{id}/keys/{key} [get]
func handleGetValueByKey(c *gin.Context) {
	db := c.MustGet("db").(*database.Database)
	objectID := c.Param("id")
	key := c.Param("key")

	result, err := db.GetValueByKey(c.Request.Context(), objectID, key)
	if err != nil {
		ServerError(c, fmt.Errorf("failed to get value by key: %w", err))
		return
	}

	if result == nil {
		NotFound(c, "key not found")
		return
	}

	// Convert keyHash to hex for JSON response
	response := map[string]interface{}{
		"value":     result.Value,
		"keyHash":   hex.EncodeToString(result.KeyHash),
		"timestamp": result.Timestamp.Format(time.RFC3339Nano),
		"slot":      result.Slot,
	}

	c.JSON(http.StatusOK, response)
}

// handleGetValueByKeyHash godoc
// @Summary      Get Value Hash by Key Hash
// @Description  Retrieves the current value hash for a specific key hash, along with timestamp and slot information.
// @Tags         objects
// @Accept       json
// @Produce      json
// @Param        keyHash     path      string  true   "Key hash (hex encoded)"
// @Success      200  {object}  database.ValueHashWithTimestamp "Value hash with timestamp and slot"
// @Failure      400  {object}  map[string]string "Invalid key hash format"
// @Failure      404  {object}  map[string]string "Key not found"
// @Failure      500  {object}  map[string]string "Internal server error"
// @Router       /keys/{keyHash} [get]
func handleGetValueByKeyHash(c *gin.Context) {
	db := c.MustGet("db").(*database.Database)
	keyHashHex := c.Param("keyHash")

	// Decode hex string to bytes
	keyHash, err := hex.DecodeString(keyHashHex)
	if err != nil {
		BadRequest(
			c,
			fmt.Errorf("invalid key hash format: must be hex encoded"),
		)
		return
	}

	result, err := db.GetValueHashByKeyHash(c.Request.Context(), keyHash)
	if err != nil {
		ServerError(
			c,
			fmt.Errorf("failed to get value hash by key hash: %w", err),
		)
		return
	}

	if result == nil {
		NotFound(c, "key not found")
		return
	}

	// Convert value hash to hex for JSON response
	response := map[string]interface{}{
		"valueHash": hex.EncodeToString(result.ValueHash),
		"proof":     result.Proof,
		"timestamp": result.Timestamp.Format(time.RFC3339Nano),
		"slot":      result.Slot,
	}

	c.JSON(http.StatusOK, response)
}

// parsePageParams extracts and validates limit and offset parameters
func parsePageParams(
	c *gin.Context,
	cfg *config.Config,
) (limit, offset int, err error) {
	limitStr := c.Query("limit")
	limit = cfg.Server.DefaultPageLimit
	if limitStr != "" {
		parsedLimit, parseErr := strconv.Atoi(limitStr)
		if parseErr != nil {
			return 0, 0, fmt.Errorf("invalid limit parameter: must be a number")
		}
		if parsedLimit <= 0 {
			return 0, 0, fmt.Errorf(
				"invalid limit parameter: must be greater than 0",
			)
		}
		if parsedLimit > cfg.Server.MaxPageLimit {
			return 0, 0, fmt.Errorf(
				"invalid limit parameter: maximum allowed is %d",
				cfg.Server.MaxPageLimit,
			)
		}
		limit = parsedLimit
	}

	offsetStr := c.Query("offset")
	offset = 0
	if offsetStr != "" {
		parsedOffset, parseErr := strconv.Atoi(offsetStr)
		if parseErr != nil {
			return 0, 0, fmt.Errorf(
				"invalid offset parameter: must be a number",
			)
		}
		if parsedOffset < 0 {
			return 0, 0, fmt.Errorf(
				"invalid offset parameter: must be greater than or equal to 0",
			)
		}
		offset = parsedOffset
	}

	return limit, offset, nil
}

// handleGetCostStatistics godoc
// @Summary      Get Cost Statistics
// @Description  Retrieves transaction fee statistics including total fees, averages, min/max values, and slot information.
// @Tags         statistics
// @Accept       json
// @Produce      json
// @Success      200  {object}  database.CostStatistics "Cost statistics"
// @Failure      500  {object}  map[string]string "Internal server error"
// @Router       /statistics/costs [get]
func handleGetCostStatistics(c *gin.Context) {
	db := c.MustGet("db").(*database.Database)

	stats, err := db.GetCostStatistics(c.Request.Context())
	if err != nil {
		ServerError(c, fmt.Errorf("failed to get cost statistics: %w", err))
		return
	}

	c.JSON(http.StatusOK, stats)
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

func parseRFC3339Flexible(s string) (time.Time, error) {
	if t, err := time.Parse(time.RFC3339Nano, s); err == nil {
		return t, nil
	}
	return time.Parse(time.RFC3339, s)
}
