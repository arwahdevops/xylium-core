# Xylium 🚀

**Xylium: The Ultra-Fast, Secure, and Stable Go Framework. Engineered for Maximum Performance with a Slim Yet Powerful Codebase.**

Xylium is a new-generation Go web framework, built on [fasthttp](https://github.com/valyala/fasthttp), that prioritizes **security**, **system stability**, **raw performance**, and **development speed** without sacrificing ease of use. With an exceptionally slim core codebase, Xylium gives you the full power of `fasthttp` through an expressive, modern API, complemented by an ecosystem of **Advanced Connectors** that are continuously updated and expanded.

## Table of Contents

*   [🔥 Why Xylium? A Rock-Solid Foundation for Your Applications](#-why-xylium-a-rock-solid-foundation-for-your-applications)
    *   [🚄 Extreme Performance & Memory Efficiency](#-extreme-performance--memory-efficiency)
    *   [🛡️ Security as a Top Priority](#️-security-as-a-top-priority)
    *   [⚙️ Battle-Tested System Stability](#️-battle-tested-system-stability)
    *   [💡 Slim Codebase, Powerful Features](#-slim-codebase-powerful-features)
    *   [🔌 Advanced & Evolving Connector Ecosystem](#-advanced--evolving-connector-ecosystem)
*   [✨ Key Features at a Glance](#-key-features-at-a-glance)
*   [🚀 Getting Started with Xylium](#-getting-started-with-xylium)
    *   [Prerequisites](#prerequisites)
    *   [Installation](#installation)
    *   [Quick & Secure "Hello World" Example](#quick--secure-hello-world-example)
*   [📖 Comprehensive Documentation](#-comprehensive-documentation)
*   [🧩 Xylium Connectors](#-xylium-connectors)
*   [🛣️ Roadmap & Contributing](#️-roadmap--contributing)
*   [💬 Community](#-community)
*   [📜 License](#-license)

## 🔥 Why Xylium? A Rock-Solid Foundation for Your Applications

Xylium isn't just another fast framework; it's designed from the ground up with core principles that ensure your applications run reliably and securely:

*   🚄 **Extreme Performance & Memory Efficiency:**
    *   Built directly on `fasthttp`, one of Go's fastest HTTP engines.
    *   Aggressive use of `sync.Pool` for `xylium.Context` and other internal objects, minimizing memory allocations and GC overhead.
    *   Optimized Radix Tree routing for high-speed route matching.
    *   **The Result:** Low latency, high throughput, and a minimal memory footprint.

*   🛡️ **Security as a Top Priority:**
    *   **Built-in Security Middleware:** CSRF protection (Double Submit Cookie with constant-time token comparison), robust CORS management. We encourage best practices like setting security headers (XSS, Content Sniffing, Frame Options) through custom middleware or application logic.
    *   **Integrated Input Validation:** Secure data binding with validation powered by `go-playground/validator/v10` to prevent malicious input.
    *   **Leak-Resistant Design:** Careful Go context management and comprehensive graceful shutdown help prevent resource leaks.
    *   **No Dangerous "Magic":** A transparent and easily auditable core codebase.

*   ⚙️ **Battle-Tested System Stability:**
    *   **Comprehensive Graceful Shutdown:** Handles OS signals (SIGINT, SIGTERM) to finish active requests and **clean up all registered resources** (including those managed by connectors) before exiting.
    *   **Centralized Error & Panic Handling:** Robust `GlobalErrorHandler` and `PanicHandler` ensure errors are handled consistently and don't crash the server. Stack traces are logged for debugging.
    *   **Operating Modes (Debug, Test, Release):** Customizable framework behavior (logging, error detail) for development, testing, and production environments, enhancing predictability.

*   💡 **Slim Codebase, Powerful Features:**
    *   **Minimalist Core:** Xylium-core remains focused on essential web functionalities, keeping it lightweight and easy to understand.
    *   **Expressive & Modern API:** Inspired by popular frameworks, reducing boilerplate and boosting developer productivity without sacrificing control.
    *   **Full Go `context.Context` Integration:** Seamless context propagation for cancellation, deadlines, and request-scoped values—critical for microservices architectures.

*   🔌 **Advanced & Evolving Connector Ecosystem:**
    *   **Effortless Integration:** Separate connector modules simplify connections to databases, tracing systems, and other third-party services.
    *   **Best Practices Built-In:** Connectors automatically leverage Xylium's `c.GoContext()` and `c.Logger()` for consistency and observability.
    *   **Lifecycle Management:** Connectors implementing `io.Closer` are automatically managed by Xylium's graceful shutdown when stored in the `appStore`.
    *   **Always Updated & Expanding:** We are committed to continuously updating existing connectors and adding support for new popular services based on community needs. *(See the [Xylium Connectors](#-xylium-connectors) section below and the [Xylium Connectors Documentation](Docs/XyliumConnectors.md) for more details).*

## ✨ Key Features at a Glance

*   **Fast Routing:** Radix tree with named parameters and catch-all routes.
*   **Flexible Middleware:** Global, group, or per-route. Includes essential middleware like Logger, Gzip, CORS, CSRF, BasicAuth, RateLimiter, RequestID, and Timeout.
*   **Data Binding & Validation:** JSON, XML, Form, Query to Go structs with tag-based validation.
*   **Contextual Logger:** `app.Logger()` and `c.Logger()` with structured output (Text/JSON) and configurable levels.
*   **Full Server Configuration:** Control over `fasthttp.Server` via `xylium.ServerConfig`.
*   **Static File Serving:** Secure and efficient.
*   **HTTPS Support:** Easily enabled.
*   **Extensible with Connectors:** Seamless integration with popular services like GORM and OpenTelemetry.

## 🚀 Getting Started with Xylium

### Prerequisites

*   Go version 1.24.2 or higher (Recommended: Latest stable Go version)

### Installation

```bash
go get -u github.com/arwahdevops/xylium-core
```

### Quick & Secure "Hello World" Example

```go
package main

import (
	"net/http"
	"time" // For Timeout example

	"github.com/arwahdevops/xylium-core/src/xylium"
)

func main() {
	// Xylium.SetMode(xylium.ReleaseMode) // Uncomment for production mode

	app := xylium.New()

	// Basic middleware for security and observability
	app.Use(xylium.RequestID())             // Add a unique ID to each request
	app.Use(xylium.Timeout(15 * time.Second)) // Request timeout
	// app.Use(xylium.CSRF())               // Enable CSRF protection (further configuration might be needed for SPAs etc.)

	app.GET("/", func(c *xylium.Context) error {
		// c.Logger() automatically includes request_id if RequestID middleware is used
		c.Logger().Infof("Request received for path: %s, RequestID: %s", c.Path(), c.MustGet(xylium.ContextKeyRequestID))
		return c.JSON(http.StatusOK, xylium.M{"message": "Hello from Secure & Fast Xylium!"})
	})

	app.Logger().Infof("Xylium Server (%s mode) starting on :8080", app.CurrentMode())
	// app.Start() includes graceful shutdown
	if err := app.Start(":8080"); err != nil {
		app.Logger().Fatalf("Failed to start server: %v", err)
	}
}
```

*(See the [Unified Showcase Example](examples/unified_showcase.go) for a more comprehensive demonstration of Xylium Core features.)*

## 📖 Comprehensive Documentation

Explore the full potential of Xylium Core through our detailed documentation:

*   **Server Basics:** `Docs/ServerBasics.md`
*   **Routing:** `Docs/Routing.md`
*   **Request Handling:** `Docs/RequestHandling.md`
*   **Response Handling:** `Docs/ResponseHandling.md`
*   **Data Binding & Validation:** `Docs/ContextBinding.md`
*   **Middleware:** `Docs/Middleware.md`
*   **Logging:** `Docs/Logging.md`
*   **Error Handling:** `Docs/ErrorHandling.md`
*   **Go Context Integration:** `Docs/GoContextIntegration.md`
*   **Advanced Configuration:** `Docs/AdvancedConfiguration.md`
*   **General Connector Philosophy:** `Docs/XyliumConnectors.md`

## 🧩 Xylium Connectors

Xylium extends its capabilities through a growing ecosystem of dedicated connectors. These connectors provide seamless, Xylium-idiomatic integration with popular databases, tracing systems, and other services.

**Available Connectors:**

*   **`xylium-gorm`**:
    *   **Purpose:** Effortless integration with [GORM](https://gorm.io/), the popular Go ORM.
    *   **Features:** Automatic Go Context propagation, contextual logging via `xylium.Logger`, connection pool management, graceful shutdown.
    *   **Repository:** [github.com/arwahdevops/xylium-gorm](https://github.com/arwahdevops/xylium-gorm)

*   **`xylium-otel`**:
    *   **Purpose:** Comprehensive OpenTelemetry integration for distributed tracing.
    *   **Features:** Simplified OTel SDK setup, automatic HTTP request instrumentation via middleware, Xylium logger integration for trace/span ID correlation, graceful shutdown.
    *   **Repository:** [github.com/arwahdevops/xylium-otel](https://github.com/arwahdevops/xylium-otel)

*(For a general overview of the connector philosophy and how to use them, see [Docs/XyliumConnectors.md](Docs/XyliumConnectors.md).*

## 🛣️ Roadmap & Contributing

Xylium is an actively developed project. We are always looking for ways to improve performance, security, and the developer experience.

**Brief Roadmap:**
*   [ ] Expansion of the Advanced Connectors list (e.g., Kafka, Redis, ElasticSearch).
*   [ ] More integrated WebSocket support within Xylium Core or as a dedicated connector.
*   [ ] CLI tool for project scaffolding and common tasks.
*   [ ] Official public benchmarks against other Go web frameworks.
*   [ ] Enhanced testing utilities for Xylium applications.

We welcome contributions of all kinds! Report bugs, suggest features, improve documentation, or submit Pull Requests. Please see `CONTRIBUTING.md` (if available) or open an issue to discuss.

## 💬 Community

*(This section can be added later if a forum, Discord, or mailing list is established for the Xylium community.)*

## 📜 License

Xylium is licensed under the [MIT License](LICENSE).
