# Xylium Connectors

Xylium Connectors are a suite of optional, standalone Go modules designed to simplify the integration of Xylium applications with various third-party services and databases. These connectors aim to reduce boilerplate code, promote best practices, and provide a Xylium-idiomatic way to interact with external systems.

## Table of Contents

*   [1. Philosophy and Goals](#1-philosophy-and-goals)
*   [2. Key Characteristics of Xylium Connectors](#2-key-characteristics-of-xylium-connectors)
*   [3. How to Use Xylium Connectors](#3-how-to-use-xylium-connectors)
*   [4. Available (or Planned) Connectors](#4-available-or-planned-connectors)
    *   [4.1. `xylium-gorm` (GORM ORM)](#41-xylium-gorm-gorm-orm)
    *   [4.2. `xylium-otel` (OpenTelemetry)](#42-xylium-otel-opentelemetry)
    *   [4.3. (Future Connectors)](#43-future-connectors)
*   [5. Developing Your Own Connector](#5-developing-your-own-connector)
*   [6. Benefits](#6-benefits)

---

## 1. Philosophy and Goals

The core Xylium framework (`xylium-core`) is designed to be lean, fast, and focused on HTTP request handling, routing, middleware, and core web functionalities. While Xylium provides excellent support for `context.Context` propagation and contextual logging (`c.Logger()`), integrating with external services like databases, message queues, or tracing systems typically requires additional setup and boilerplate from the developer.

Xylium Connectors address this by:

*   **Simplifying Integration:** Providing pre-built, Xylium-aware wrappers or helpers for popular Go libraries that interact with external services.
*   **Promoting Best Practices:** Automatically leveraging `c.GoContext()` for timeouts and cancellation, and `c.Logger()` for consistent, contextualized logging of connector operations.
*   **Reducing Boilerplate:** Handling common setup, configuration, and operational patterns associated with each service.
*   **Ensuring Consistency:** Offering a configuration and usage style that feels familiar to Xylium developers.
*   **Lifecycle Management:** Facilitating proper resource cleanup (e.g., closing database connections, flushing telemetry exporters) during Xylium's graceful shutdown. This is often achieved by connectors implementing `io.Closer` and being registered with Xylium's application store.

## 2. Key Characteristics of Xylium Connectors

*   **Standalone Modules:** Each connector is typically a separate Go module with its own repository and versioning (e.g., `github.com/arwahdevops/xylium-gorm`). This keeps `xylium-core` lean and allows connectors to evolve independently.
*   **Xylium-Aware:**
    *   They often accept `*xylium.Context` as a parameter in their operational methods (e.g., for database queries, emitting traces).
    *   They utilize `c.GoContext()` for context propagation to underlying library calls, respecting request deadlines and cancellation.
    *   They use `c.Logger()` for contextual logging of their operations (e.g., SQL queries, Redis commands, trace export status).
*   **Configurable:** Connectors provide clear configuration structs (e.g., `xyliumgorm.Config`, `xyliumotel.Config`) for easy setup.
*   **`io.Closer` Implementation (where applicable):** Connectors managing persistent connections or background processes (like database pools, telemetry exporters) typically implement the `io.Closer` interface. This allows Xylium's router to automatically manage their cleanup during graceful shutdown if the connector instance is stored using `app.AppSet(key, connectorInstance)`.
*   **High-Level API:** While providing access to the underlying library if needed, connectors often offer a slightly higher-level or more convenient API tailored for common use cases within a Xylium application.

## 3. How to Use Xylium Connectors

The general workflow for using a Xylium Connector is:

1.  **Add the Connector Module:**
    ```bash
    go get github.com/arwahdevops/xylium-<connector-name> 
    // Example: go get github.com/arwahdevops/xylium-gorm
    ```

2.  **Import the Connector Package:**
    ```go
    import (
        "github.com/arwahdevops/xylium-core/src/xylium" // Your Xylium core
        xyliumgorm "github.com/arwahdevops/xylium-gorm"  // Example: GORM connector
        // xyliumotel "github.com/arwahdevops/xylium-otel" // Example: OpenTelemetry connector
    )
    ```

3.  **Configure and Initialize the Connector:**
    This is typically done in your `main.go` or application setup function.
    ```go
    func main() {
        app := xylium.New()
        appLogger := app.Logger() // Use Xylium's app logger for connector initialization

        // Example: GORM Connector Configuration
        gormCfg := xyliumgorm.Config{
            Dialect:           xyliumgorm.Postgres, // Or other supported dialects
            DSN:               "host=localhost user=postgres password=secret dbname=mydb port=5432 sslmode=disable TimeZone=Asia/Shanghai",
            EnableGormLogging: true,
            // GormLogLevel:      gormlogger.Info, // GORM's own log level (specific to GORM connector)
            DefaultXyliumLogger: appLogger.WithFields(xylium.M{"component": "gorm-connector"}), // Pass a derived Xylium logger
            // ... other GORM specific settings ...
        }
        dbConnector, err := xyliumgorm.New(gormCfg)
        if err != nil {
            appLogger.Fatalf("Failed to initialize GORM connector: %v", err)
        }

        // Store the connector instance in Xylium's application store for easy access in handlers.
        // If dbConnector implements io.Closer, Xylium's AppSet will automatically register it 
        // for graceful shutdown (via app.RegisterCloser).
        app.AppSet("db", dbConnector)

        // ... define routes ...
        // app.GET("/users/:id", GetUserHandler) // GetUserHandler would retrieve "db" from c.AppGet("db")

        // Start the Xylium server. This will also handle graceful shutdown of 'dbConnector'.
        if err := app.Start(":8080"); err != nil {
            appLogger.Fatalf("Failed to start server: %v", err)
        }
    }
    ```

4.  **Use the Connector in Your Handlers:**
    Retrieve the connector instance from Xylium's application store (`c.AppGet(key)`) or pass it via other dependency injection methods. Then, use its Xylium-aware methods.
    ```go
    // Assume User struct and GORM instance are available
    // type User struct { gorm.Model; Name string; Email string }

    func GetUserHandler(c *xylium.Context) error {
        // Retrieve the GORM connector from the application store
        dbVal, ok := c.AppGet("db")
        if !ok {
            c.Logger().Error("Database connector 'db' not found in application store.")
            return xylium.NewHTTPError(xylium.StatusInternalServerError, "Database connector not configured")
        }
        dbConnector, ok := dbVal.(*xyliumgorm.Connector) // Type assertion
        if !ok {
            c.Logger().Errorf("Value for 'db' in app store is not a *xyliumgorm.Connector, got %T", dbVal)
            return xylium.NewHTTPError(xylium.StatusInternalServerError, "Invalid database connector type")
        }

        var user User
        userID := c.Param("id")

        // Use the connector's method, passing the Xylium context.
        // The connector internally uses c.GoContext() for database operations and c.Logger() for logging.
        // Example: dbConnector.WithContext(c) returns a *gorm.DB instance scoped with c.GoContext().
        // result := dbConnector.WithContext(c).First(&user, userID) 
        
        // Placeholder for actual GORM query execution
        // Assume result is a gorm.Result-like object or similar from the connector
        // if result.Error != nil { 
        //     if errors.Is(result.Error, gorm.ErrRecordNotFound) { // Example for GORM
        //         return xylium.NewHTTPError(xylium.StatusNotFound, "User not found")
        //     }
        //     // The GORM connector's logger (if enabled) would have already logged the SQL error.
        //     // You can add more business-context logging here.
        //     c.Logger().Errorf("Failed to retrieve user %s: %v", userID, result.Error)
        //     return xylium.NewHTTPError(xylium.StatusInternalServerError, "Error fetching user data")
        // }

        return c.JSON(xylium.StatusOK, user)
    }
    ```

## 4. Available (or Planned) Connectors

This section lists officially supported or community-recognized Xylium Connectors. Refer to each connector's own repository for detailed documentation.

### 4.1. `xylium-gorm` (GORM ORM)

*   **Purpose:** Simplifies using [GORM](https://gorm.io/), a popular Go ORM, with Xylium.
*   **Features:**
    *   Supports multiple SQL dialects (SQLite, MySQL, PostgreSQL, SQL Server via GORM drivers).
    *   Automatic propagation of `c.GoContext()` to GORM operations (e.g., `db.WithContext(c.GoContext())`).
    *   Integrated GORM query logging via `c.Logger()` (configurable levels, slow query detection).
    *   Configuration struct for easy setup of GORM and database connection pool settings.
    *   Implements `io.Closer` for graceful shutdown of the database connection pool.
*   **Repository:** `github.com/arwahdevops/xylium-gorm` (or the actual repository URL)
*   **Status:** (e.g., Alpha, Beta, Stable - to be filled by connector maintainer)

### 4.2. `xylium-otel` (OpenTelemetry)

*   **Purpose:** Provides comprehensive OpenTelemetry integration for distributed tracing and metrics.
*   **Features:**
    *   Simplified OTel SDK setup (TracerProvider, MeterProvider, Exporters).
    *   HTTP request instrumentation via Xylium middleware (extracts/injects trace context, creates spans).
    *   Integration with `xylium.Logger` to automatically include `trace_id` and `span_id` in logs when a span is active in `c.GoContext()`.
    *   Graceful shutdown of OTel exporters (implements `io.Closer`).
*   **Repository:** `github.com/arwahdevops/xylium-otel` (or the actual repository URL)
*   **Status:** (e.g., Alpha, Beta, Stable - to be filled by connector maintainer)


### 4.3. (Future Connectors)

Potential future connectors could include:

*   **`xylium-sql`**: A Xylium-aware wrapper for Go's standard `database/sql` package, providing contextual logging and `c.GoContext()` propagation for queries.
*   **`xylium-redis`**: Integration with popular Go Redis clients (e.g., `go-redis/redis`), with `c.GoContext()` propagation, command logging, and potentially a `LimiterStore` implementation for Xylium's Rate Limiter middleware.
*   **`xylium-kafka` / `xylium-rabbitmq` / `xylium-nats`**: For message queue interactions.
*   **`xylium-elasticsearch`**: For search engine integration.
*   **`xylium-s3`**: For object storage services.
*   **`xylium-grpc-client`**: Helpers for making gRPC calls with context propagation and logging.

Community contributions for new connectors are highly encouraged!

## 5. Developing Your Own Connector

If you need to integrate a service not yet covered by an official connector, you can develop your own by following these principles:

1.  **Accept `*xylium.Context`**: Your connector's methods that perform I/O or need logging should ideally accept `c *xylium.Context` as an argument where appropriate.
2.  **Use `c.GoContext()`**: Pass `c.GoContext()` to any underlying library calls that support `context.Context`. This is crucial for cancellation, deadlines, and trace propagation.
3.  **Use `c.Logger()`**: Utilize the request-scoped logger `c.Logger().WithFields(xylium.M{"connector": "my-connector-name"})` for logging operations, errors, and debug information. This ensures logs are contextualized with request IDs, trace IDs, etc.
4.  **Configuration (`Config` struct)**: Provide a clear `Config` struct for initializing your connector. If your connector needs access to a `xylium.Logger` during its own initialization (outside of a request context), consider accepting a `xylium.Logger` (e.g., `app.Logger()`) in your `NewConnector(cfg MyConfig, baseLogger xylium.Logger)` function.
5.  **Implement `io.Closer`**: If your connector manages resources that need to be released (e.g., connection pools, background goroutines, telemetry exporters), it **must** implement the `Close() error` method. This allows Xylium to manage its lifecycle during graceful shutdown if the connector instance is stored in the application store using `app.AppSet()`.
6.  **Error Handling**: Return standard Go errors from your connector's methods. The Xylium handler using your connector can then decide how to map these to `*xylium.HTTPError` if needed for client responses.
7.  **Standalone Module**: Consider releasing your connector as a separate Go module (e.g., `github.com/your-org/xylium-myconnector`) to encourage reuse and community contribution.

## 6. Benefits

*   **Reduced Boilerplate:** Less repetitive code for setting up contexts, loggers, and basic configurations for external services.
*   **Increased Productivity:** Developers can focus more on business logic rather than integration plumbing.
*   **Consistency:** Logging and context propagation are handled uniformly across different parts of the application.
*   **Maintainability:** Easier to manage dependencies and update individual service integrations.
*   **Robustness:** Encourages proper context handling and resource management (including graceful shutdown), leading to more resilient applications.
*   **Growing Ecosystem:** Makes Xylium more attractive by providing out-of-the-box solutions for common integration needs.

We encourage the community to contribute to existing connectors or develop new ones to enrich the Xylium ecosystem!
