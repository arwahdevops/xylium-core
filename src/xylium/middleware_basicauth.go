// src/xylium/middleware_basicauth.go
package xylium

import (
	"encoding/base64" // For decoding Basic Auth credentials.
	"errors"         // For defining standard error types.
	"strings"        // For string manipulation (prefix checking, splitting).
	// "fmt" is implicitly used by Errorf, Warnf, etc.
)

// BasicAuthConfig defines the configuration for the BasicAuth middleware.
type BasicAuthConfig struct {
	// Validator is a function that validates the provided username and password.
	// It must be provided by the user.
	// It should return:
	//  - user (interface{}): Optional user information to be stored in the context if auth is successful.
	//  - valid (bool): True if credentials are valid, false otherwise.
	//  - err (error): An optional error if the validation process itself fails (e.g., database error).
	//                 This is different from invalid credentials.
	Validator func(username, password string, c *Context) (user interface{}, valid bool, err error)

	// Realm is the realm string displayed in the browser's authentication dialog.
	// Defaults to "Restricted" if not set.
	Realm string

	// ErrorHandler is a custom function to handle authentication failures (e.g., missing header, invalid credentials).
	// If nil, a default handler sends StatusUnauthorized with a WWW-Authenticate header.
	// The ErrorHandler is responsible for sending the response.
	ErrorHandler HandlerFunc

	// ContextUserKey is the key used to store the authenticated user's information (returned by Validator)
	// in the Xylium Context store. Defaults to "user".
	ContextUserKey string
}

// ErrorBasicAuthInvalid is a standard error returned or used internally when Basic Auth credentials are
// determined to be invalid by the Validator, or if the auth header is malformed.
// It can be used by custom ErrorHandler implementations.
var ErrorBasicAuthInvalid = errors.New("xylium: invalid or missing basic auth credentials")

// BasicAuth returns a BasicAuth middleware with a custom validator.
// It uses the default realm "Restricted" and default error handling.
// The validator function is mandatory.
func BasicAuth(validator func(username, password string, c *Context) (interface{}, bool, error)) Middleware {
	if validator == nil {
		panic("xylium: BasicAuth middleware requires a non-nil validator function")
	}
	config := BasicAuthConfig{
		Validator: validator,
		// Realm and ErrorHandler will use defaults set in BasicAuthWithConfig.
	}
	return BasicAuthWithConfig(config)
}

// BasicAuthWithConfig returns a BasicAuth middleware configured with the provided options.
func BasicAuthWithConfig(config BasicAuthConfig) Middleware {
	// Validate mandatory configuration.
	if config.Validator == nil {
		panic("xylium: BasicAuth middleware requires a non-nil validator function in its config")
	}

	// Apply defaults for optional configuration fields.
	if config.Realm == "" {
		config.Realm = "Restricted" // Default realm.
	}
	if config.ContextUserKey == "" {
		config.ContextUserKey = "user" // Default key for storing user info in context.
	}

	// Define the default error handler if none is provided by the user.
	// This handler sends a 401 Unauthorized response and the WWW-Authenticate header
	// to prompt the browser for credentials.
	defaultErrorHandler := func(c *Context) error {
		c.SetHeader("WWW-Authenticate", `Basic realm="`+config.Realm+`"`)
		// The internal error ErrorBasicAuthInvalid provides more context for logs.
		return NewHTTPError(StatusUnauthorized, "Unauthorized.").WithInternal(ErrorBasicAuthInvalid)
	}

	// Use the user-provided ErrorHandler if available, otherwise use the default.
	errorHandlerToUse := config.ErrorHandler
	if errorHandlerToUse == nil {
		errorHandlerToUse = defaultErrorHandler
	}

	// Return the actual middleware handler function.
	return func(next HandlerFunc) HandlerFunc {
		return func(c *Context) error {
			// Get a request-scoped logger. This will include request_id if available.
			logger := c.Logger()

			// Retrieve the Authorization header from the request.
			authHeader := c.Header("Authorization")

			// If the Authorization header is missing, invoke the error handler.
			if authHeader == "" {
				logger.Debugf("BasicAuth: Authorization header missing for %s %s", c.Method(), c.Path())
				return errorHandlerToUse(c)
			}

			// Check if the header uses the "Basic " prefix.
			const prefix = "Basic "
			if !strings.HasPrefix(authHeader, prefix) {
				logger.Warnf("BasicAuth: Invalid Authorization header format (missing 'Basic ' prefix) for %s %s", c.Method(), c.Path())
				return errorHandlerToUse(c)
			}

			// Decode the base64 encoded credentials.
			encodedCredentials := authHeader[len(prefix):]
			credentials, err := base64.StdEncoding.DecodeString(encodedCredentials)
			if err != nil {
				// Failed to decode credentials. This could be due to a malformed header.
				logger.Warnf("BasicAuth: Failed to decode base64 credentials for %s %s: %v", c.Method(), c.Path(), err)
				return errorHandlerToUse(c)
			}

			// Split the decoded credentials into username and password.
			// The expected format is "username:password".
			parts := strings.SplitN(string(credentials), ":", 2)
			if len(parts) != 2 {
				// Invalid credentials format (e.g., missing colon, or only username).
				logger.Warnf("BasicAuth: Invalid credentials format (expected 'username:password') for %s %s", c.Method(), c.Path())
				return errorHandlerToUse(c)
			}
			username, password := parts[0], parts[1]

			// Call the user-provided Validator function.
			// The validator will determine if the credentials are correct.
			user, valid, validationErr := config.Validator(username, password, c)

			if validationErr != nil {
				// An error occurred within the validator function itself (e.g., database connection issue).
				// This is treated as a server-side problem, not just invalid credentials.
				logger.Errorf("BasicAuth: Validator function returned an error for user '%s', path %s %s: %v",
					username, c.Method(), c.Path(), validationErr)
				// Return a generic 500 error, a more specific error could be returned by the validator
				// and handled by the global error handler if preferred.
				return NewHTTPError(StatusInternalServerError, "Authentication check failed due to an internal error.").WithInternal(validationErr)
			}

			if !valid {
				// Credentials provided were not valid as per the Validator.
				logger.Warnf("BasicAuth: Invalid credentials provided for user '%s' for %s %s", username, c.Method(), c.Path())
				return errorHandlerToUse(c)
			}

			// Authentication successful.
			// If the validator returned user information, store it in the context.
			if user != nil {
				c.Set(config.ContextUserKey, user)
				logger.Debugf("BasicAuth: User '%s' authenticated successfully for %s %s. User info stored in context key '%s'.",
					username, c.Method(), c.Path(), config.ContextUserKey)
			} else {
				logger.Debugf("BasicAuth: User '%s' authenticated successfully for %s %s. No user info returned by validator to store.",
					username, c.Method(), c.Path())
			}


			// Proceed to the next handler in the chain.
			return next(c)
		}
	}
}
