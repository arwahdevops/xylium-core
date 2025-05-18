package xylium

import (
	"encoding/base64" // For decoding Basic Auth credentials.
	"errors"         // For defining standard error types.
	"strings"        // For string manipulation (prefix checking, splitting).
)

// BasicAuthConfig defines the configuration for the BasicAuth middleware.
// This middleware provides HTTP Basic Authentication as defined in RFC 7617.
type BasicAuthConfig struct {
	// Validator is a mandatory function provided by the user to validate
	// the parsed username and password against a credential store (e.g., database, config).
	// It should return:
	//  - user (interface{}): Optional user information (e.g., user ID, roles) to be stored
	//                        in the Xylium Context if authentication is successful. This data
	//                        can then be accessed by subsequent handlers.
	//  - valid (bool): True if the provided username and password are valid, false otherwise.
	//  - err (error): An optional error if the validation process itself fails (e.g., database
	//                 connection error). This is distinct from simply invalid credentials.
	//                 If err is non-nil, it's typically treated as a server-side error.
	Validator func(username, password string, c *Context) (user interface{}, valid bool, err error)

	// Realm is the realm string displayed in the browser's authentication dialog
	// when a 401 Unauthorized response is sent. It helps users understand which
	// credentials are being requested.
	// Defaults to "Restricted" if not set.
	Realm string

	// ErrorHandler is a custom function to handle authentication failures. This includes
	// scenarios like a missing Authorization header, malformed credentials, or invalid credentials.
	// If nil, a default handler sends an HTTP 401 Unauthorized response with a
	// WWW-Authenticate header (including the Realm) to prompt the browser for credentials.
	// The ErrorHandler is responsible for sending the complete response to the client.
	ErrorHandler HandlerFunc

	// ContextUserKey is the key used to store the authenticated user's information
	// (the `user` interface{} returned by a successful `Validator` call) in the
	// Xylium Context store (`c.store`). Subsequent handlers can retrieve this information
	// using `c.Get(config.ContextUserKey)`.
	// Defaults to "user".
	ContextUserKey string
}

// ErrorBasicAuthInvalid is a standard error that can be used by custom ErrorHandler implementations
// or logged when Basic Auth credentials are determined to be invalid by the Validator,
// or if the Authorization header is malformed or missing.
var ErrorBasicAuthInvalid = errors.New("xylium: invalid or missing basic auth credentials")

// BasicAuth returns a BasicAuth middleware with a custom validator.
// It uses the default realm ("Restricted"), default error handling (401 with WWW-Authenticate),
// and default context key ("user"). The validator function is mandatory.
//
// Deprecated: Users should prefer BasicAuthWithConfig for clarity and future-proofing,
// even if only the Validator is being set. This function may be removed in a future version.
// For now, it provides a simpler entry point if only the validator is needed with all other defaults.
func BasicAuth(validator func(username, password string, c *Context) (interface{}, bool, error)) Middleware {
	if validator == nil {
		// Panic early if the essential validator is missing.
		panic("xylium: BasicAuth middleware requires a non-nil validator function")
	}
	config := BasicAuthConfig{
		Validator: validator,
		// Realm, ErrorHandler, and ContextUserKey will use defaults defined in BasicAuthWithConfig.
	}
	return BasicAuthWithConfig(config)
}

// BasicAuthWithConfig returns a BasicAuth middleware configured with the provided options.
// It handles parsing the "Authorization: Basic <credentials>" header, decoding credentials,
// invoking the user-supplied validator, and managing the response on success or failure.
func BasicAuthWithConfig(config BasicAuthConfig) Middleware {
	// Validate mandatory configuration: Validator function must be provided.
	if config.Validator == nil {
		panic("xylium: BasicAuth middleware requires a non-nil validator function in its config")
	}

	// Apply defaults for optional configuration fields if they are not set by the user.
	if config.Realm == "" {
		config.Realm = "Restricted" // Default realm string for WWW-Authenticate header.
	}
	if config.ContextUserKey == "" {
		config.ContextUserKey = "user" // Default key for storing user info in `c.store`.
	}

	// Define the default error handler if no custom ErrorHandler is provided.
	// This handler sends a 401 Unauthorized response and the WWW-Authenticate header
	// to prompt the browser for credentials, or to indicate failure to an API client.
	defaultErrorHandler := func(c *Context) error {
		// Set the WWW-Authenticate header to indicate Basic Auth is required for the specified realm.
		c.SetHeader("WWW-Authenticate", `Basic realm="`+config.Realm+`"`)
		// Return an HTTPError. The GlobalErrorHandler will process this, sending a 401 status
		// and a JSON body (by default) like {"error": "Unauthorized."}.
		// ErrorBasicAuthInvalid provides internal context for logging.
		return NewHTTPError(StatusUnauthorized, "Unauthorized.").WithInternal(ErrorBasicAuthInvalid)
	}

	// Use the user-provided ErrorHandler if available; otherwise, use the default one.
	errorHandlerToUse := config.ErrorHandler
	if errorHandlerToUse == nil {
		errorHandlerToUse = defaultErrorHandler
	}

	// Return the actual middleware handler function that forms the core of the BasicAuth logic.
	return func(next HandlerFunc) HandlerFunc {
		return func(c *Context) error {
			// Get a request-scoped logger. This will include request_id if available.
			logger := c.Logger().WithFields(M{"middleware": "BasicAuth"}) // Add middleware context to logger.

			// Retrieve the Authorization header from the request.
			authHeader := c.Header("Authorization")

			// If the Authorization header is missing, authentication cannot proceed.
			// Invoke the configured error handler.
			if authHeader == "" {
				logger.Debugf("Authorization header missing for %s %s. Invoking error handler.", c.Method(), c.Path())
				return errorHandlerToUse(c)
			}

			// The Authorization header for Basic Auth must start with "Basic ".
			const prefix = "Basic "
			if !strings.HasPrefix(authHeader, prefix) {
				logger.Warnf("Invalid Authorization header format (missing '%s' prefix) for %s %s. Invoking error handler.", prefix, c.Method(), c.Path())
				return errorHandlerToUse(c)
			}

			// Extract the base64 encoded credentials part from the header.
			encodedCredentials := authHeader[len(prefix):]
			// Decode the base64 string.
			credentials, err := base64.StdEncoding.DecodeString(encodedCredentials)
			if err != nil {
				// Failed to decode credentials, likely due to a malformed base64 string.
				logger.Warnf("Failed to decode base64 credentials for %s %s: %v. Invoking error handler.", c.Method(), c.Path(), err)
				return errorHandlerToUse(c)
			}

			// Split the decoded credentials into username and password.
			// The expected format is "username:password".
			parts := strings.SplitN(string(credentials), ":", 2)
			if len(parts) != 2 {
				// Invalid credentials format (e.g., missing colon, or only username/password).
				logger.Warnf("Invalid credentials format (expected 'username:password') after base64 decoding for %s %s. Invoking error handler.", c.Method(), c.Path())
				return errorHandlerToUse(c)
			}
			username, password := parts[0], parts[1]

			// Call the user-provided Validator function with the parsed username and password.
			user, valid, validationErr := config.Validator(username, password, c)

			if validationErr != nil {
				// An error occurred within the validator function itself (e.g., database issue).
				// This is treated as a server-side problem, not merely invalid credentials.
				// Log the error with details.
				logger.Errorf("Validator function returned an error for user '%s', path %s %s: %v",
					username, c.Method(), c.Path(), validationErr)
				// Return a generic 500 Internal Server Error. The GlobalErrorHandler will handle this.
				// The `validationErr` is included as the internal cause for detailed logging.
				return NewHTTPError(StatusInternalServerError, "Authentication check failed due to an internal server error.").WithInternal(validationErr)
			}

			if !valid {
				// Credentials provided were not valid as determined by the Validator.
				// Log the failed authentication attempt.
				logger.Warnf("Invalid credentials provided for user '%s' for %s %s. Invoking error handler.", username, c.Method(), c.Path())
				return errorHandlerToUse(c)
			}

			// --- Authentication Successful ---
			// If the validator returned user information, store it in the context
			// using the configured ContextUserKey.
			if user != nil {
				c.Set(config.ContextUserKey, user)
				logger.Debugf("User '%s' authenticated successfully for %s %s. User info stored in context key '%s'.",
					username, c.Method(), c.Path(), config.ContextUserKey)
			} else {
				logger.Debugf("User '%s' authenticated successfully for %s %s. No user info returned by validator to store in context.",
					username, c.Method(), c.Path())
			}

			// Authentication was successful. Proceed to the next handler in the chain.
			return next(c)
		}
	}
}
