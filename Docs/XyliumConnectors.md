# Xylium Connectors

Xylium Connectors are a suite of optional, standalone Go modules designed to simplify the integration of Xylium applications with various third-party services and databases. These connectors aim to reduce boilerplate code, promote best practices, and provide a Xylium-idiomatic way to interact with external systems.

## Table of Contents

*   [1. Philosophy and Goals](#1-philosophy-and-goals)
*   [2. Key Characteristics of Xylium Connectors](#2-key-characteristics-of-xylium-connectors)
*   [3. How to Use Xylium Connectors](#3-how-to-use-xylium-connectors)
*   [4. Available (or Planned) Connectors](#4-available-or-planned-connectors)
    *   [4.1. `xylium-gorm` (GORM ORM)](#41-xylium-gorm-gorm-orm)
    *   [4.2. `xylium-sql` (database/sql)](#42-xylium-sql-databasesql)
    *   [4.3. `xylium-redis`](#43-xylium-redis)
    *   [4.4. (Future Connectors)](#44-future-connectors)
*   [5. Developing Your Own Connector](#5-developing-your-own-connector)
*   [6. Benefits](#6-benefits)

---

## 1. Philosophy and Goals

The core Xylium framework (`xylium-core`) is designed to be lean, fast, and focused on HTTP request handling, routing, middleware, and core web functionalities. While Xylium provides excellent support for `context.Context` propagation and contextual logging (`c.Logger()`), integrating with external services like databases, message queues, or caches typically requires additional setup and boilerplate from the developer.

Xylium Connectors address this by:

*   **Simplifying Integration:** Providing pre-built, Xylium-aware wrappers or helpers for popular Go libraries that interact with external services.
*   **Promoting Best Practices:** Automatically leveraging `c.GoContext()` for timeouts and cancellation, and `c.Logger()` for consistent, contextualized logging of connector operations.
*   **Reducing Boilerplate:** Handling common setup, configuration, and operational patterns associated with each service.
*   **Ensuring Consistency:** Offering a configuration and usage style that feels familiar to Xylium developers.
*   **Lifecycle Management:** Facilitating proper resource cleanup (e.g., closing database connections) during Xylium's graceful shutdown when connectors are managed via the application store.

## 2. Key Characteristics of Xylium Connectors

*   **Standalone Modules:** Each connector is a separate Go module with its own repository and versioning (e.g., `github.com/arwahdevops/xylium-gorm`). This keeps `xylium-core` lean and allows connectors to evolve independently.
*   **Xylium-Aware:**
    *   They accept `*xylium.Context` as a parameter in their operational methods.
    *   They utilize `c.GoContext()` for context propagation to underlying library calls.
    *   They use `c.Logger()` for contextual logging of their operations (e.g., SQL queries, Redis commands).
*   **Configurable:** Connectors provide clear configuration structs (e.g., `xyliumgorm.Config`) for easy setup.
*   **`io.Closer` Implementation (where applicable):** Connectors managing persistent connections (like database pools) typically implement the `io.Closer` interface. This allows Xylium's `Router.AppSet()` to automatically register them for cleanup during graceful shutdown if the connector instance is stored in the application store.
*   **High-Level API:** While providing access to the underlying library if needed, connectors often offer a slightly higher-level or more convenient API tailored for common use cases within a Xylium application.

## 3. How to Use Xylium Connectors

The general workflow for using a Xylium Connector is:

1.  **Add the Connector Module:**
    ```bash
    go get github.com/arwahdevops/xylium-<connector-name>
    ```

2.  **Import the Connector Package:**
    ```go
    import (
        "github.com/arwahdevops/xylium-core/src/xylium" // Your Xylium core
        xyliumgorm "github.com/arwahdevops/xylium-gorm"  // Example: GORM connector
    )
    ```

3.  **Configure and Initialize the Connector:**
    This is typically done in your `main.go` or application setup function.
    ```go
    func main() {
        app := xylium.New()
        appLogger := app.Logger() // Use Xylium's app logger for connector initialization

        gormCfg := xyliumgorm.Config{
            Dialect:           xyliumgorm.Postgres,
            DSN:               "host=localhost user=postgres password=secret dbname=mydb port=5432 sslmode=disable",
            EnableGormLogging: true,
            GormLogLevel:      gormlogger.Info, // GORM's own log level
            DefaultXyliumLogger: appLogger, // Pass Xylium logger to GORM adapter
            // ... other GORM specific settings ...
        }
        dbConnector, err := xyliumgorm.New(gormCfg)
        if err != nil {
            appLogger.Fatalf("Failed to initialize GORM connector: %v", err)
        }

        // Store the connector instance in Xylium's application store for easy access in handlers.
        // If dbConnector implements io.Closer, AppSet will also register it for graceful shutdown.
        app.AppSet("db", dbConnector)

        // ... define routes ...
        // app.GET("/users/:id", GetUserHandler)

        app.Start(":8080") // This will also handle graceful shutdown of 'dbConnector'
    }
    ```

4.  **Use the Connector in Your Handlers:**
    Retrieve the connector instance from Xylium's application store (or pass it via other DI methods) and use its Xylium-aware methods.
    ```go
    func GetUserHandler(c *xylium.Context) error {
        dbVal, ok := c.AppGet("db")
        if !ok {
            // This should not happen if initialized correctly
            return xylium.NewHTTPError(http.StatusInternalServerError, "Database connector not found")
        }
        dbConnector := dbVal.(*xyliumgorm.Connector) // Type assertion

        var user User
        // Use the connector's method, passing the Xylium context
        // The connector internally uses c.GoContext() and c.Logger()
        result := dbConnector.WithContext(c).First(&user, c.Param("id"))
        if result.Error != nil {
            if errors.Is(result.Error, gorm.ErrRecordNotFound) {
                return xylium.NewHTTPError(http.StatusNotFound, "User not found")
            }
            // The GORM connector's logger (if enabled) would have already logged the SQL error.
            // You can add more business-context logging here.
            c.Logger().Errorf("Failed to retrieve user %s: %v", c.Param("id"), result.Error)
            return xylium.NewHTTPError(http.StatusInternalServerError, "Error fetching user data")
        }

        return c.JSON(http.StatusOK, user)
    }
    ```

## 4. Available (or Planned) Connectors

This section will list officially supported or community-recognized Xylium Connectors.

### 4.1. `xylium-gorm` (GORM ORM)

*   **Purpose:** Simplifies using [GORM](https://gorm.io/), a popular Go ORM, with Xylium.
*   **Features:**
    *   Supports multiple SQL dialects (SQLite, MySQL, PostgreSQL via GORM drivers).
    *   Automatic propagation of `c.GoContext()` to GORM operations.
    *   Integrated GORM query logging via `c.Logger()` (configurable levels, slow query detection).
    *   Configuration struct for easy setup of GORM and database connection pool.
    *   Implements `io.Closer` for graceful shutdown of the database connection pool.
*   **Repository:** `github.com/arwahdevops/xylium-gorm` (Example path)
*   **Status:** (e.g., Alpha, Beta, Stable)

### 4.2. `xylium-sql` (database/sql)

*   **Purpose:** Provides a Xylium-aware wrapper around Go's standard `database/sql` package.
*   **Features:**
    *   Automatic propagation of `c.GoContext()` to `DB.QueryContext`, `DB.ExecContext`, etc.
    *   Integrated SQL query logging via `c.Logger()`.
    *   Helper for managing connection pool settings.
    *   Implements `io.Closer`.
*   **Repository:** `github.com/arwahdevops/xylium-sql` (Example path)
*   **Status:** (e.g., Planned, In Development)

### 4.3. `xylium-redis`

*   **Purpose:** Simplifies interaction with Redis key-value store using popular Go Redis clients (e.g., `go-redis/redis`).
*   **Features:**
    *   Automatic propagation of `c.GoContext()` to Redis commands.
    *   Integrated command logging via `c.Logger()`.
    *   Configuration for connection options.
    *   Implements `io.Closer`.
    *   Potential to offer a `LimiterStore` implementation for Xylium's Rate Limiter middleware.
*   **Repository:** `github.com/arwahdevops/xylium-redis` (Example path)
*   **Status:** (e.g., Planned)

### 4.4. (Future Connectors)

Potential future connectors could include:

*   `xylium-kafka` / `xylium-rabbitmq` / `xylium-nats` (Message Queues)
*   `xylium-elasticsearch` (Search Engines)
*   `xylium-s3` (Object Storage)
*   `xylium-grpc-client` (gRPC Client Helpers)
*   And more, based on community needs.

## 5. Developing Your Own Connector

If you need to integrate a service not yet covered by an official connector, you can develop your own by following these principles:

1.  **Accept `*xylium.Context`:** Your connector's methods that perform I/O or need logging should accept `c *xylium.Context` as an argument.
2.  **Use `c.GoContext()`:** Pass `c.GoContext()` to any underlying library calls that support `context.Context`. This is crucial for cancellation and deadlines.
3.  **Use `c.Logger()`:** Use the request-scoped logger `c.Logger().WithFields(xylium.M{"connector": "my-connector"})` for logging operations, errors, and debug information.
4.  **Configuration:** Provide a `Config` struct for initializing your connector. If your connector needs access to a `xylium.Logger` during its own initialization (outside of a request context), consider accepting a `xylium.Logger` (e.g., `app.Logger()`) in your `NewConnector(cfg MyConfig, baseLogger xylium.Logger)` function.
5.  **Implement `io.Closer`:** If your connector manages resources that need to be released (e.g., connection pools, background goroutines), implement the `Close() error` method. This allows Xylium to manage its lifecycle during graceful shutdown if the instance is stored using `app.AppSet()`.
6.  **Error Handling:** Return standard Go errors from your connector's methods. The Xylium handler using your connector can then decide how to map these to `*xylium.HTTPError` if needed.
7.  **Standalone Module:** Consider releasing your connector as a separate Go module to encourage reuse and community contribution.

## 6. Benefits

*   **Reduced Boilerplate:** Less repetitive code for setting up contexts, loggers, and basic configurations for external services.
*   **Increased Productivity:** Developers can focus more on business logic rather than integration plumbing.
*   **Consistency:** Logging and context propagation are handled uniformly across different parts of the application.
*   **Maintainability:** Easier to manage dependencies and update individual service integrations.
*   **Robustness:** Encourages proper context handling and resource management, leading to more resilient applications.
*   **Growing Ecosystem:** Makes Xylium more attractive by providing out-of-the-box solutions for common integration needs.

We encourage the community to contribute to existing connectors or develop new ones to enrich the Xylium ecosystem!
