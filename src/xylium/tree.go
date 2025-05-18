package xylium

import (
	"fmt"     // For formatting error messages and route printing.
	"sort"    // For sorting child nodes and methods for consistent behavior/output.
	"strings" // For path manipulation (splitting, joining, replacing).
)

// nodeType defines the type of a node in the radix tree, influencing matching priority.
// Static nodes have the highest priority, followed by parameter nodes, then catch-all nodes.
type nodeType uint8

const (
	staticNode   nodeType = iota // Represents a static path segment (e.g., "/users", "/products").
	paramNode                    // Represents a named path parameter (e.g., "/users/:id", "/items/:category").
	catchAllNode                 // Represents a catch-all parameter, must be at the end of a path (e.g., "/static/*filepath").
)

// routeTarget holds the endpoint-specific handler and middleware for a route
// associated with a particular HTTP method on a tree node.
type routeTarget struct {
	handler    HandlerFunc  // The main request handler function: `func(*Context) error`.
	middleware []Middleware // Slice of middleware specific to this particular route and method.
}

// node represents a node in the radix tree. Each node corresponds to a segment
// of a URL path and can have associated handlers for different HTTP methods.
type node struct {
	path      string                 // The path segment this node represents (e.g., "users", ":id", "*filepath").
	children  []*node                // Child nodes, sorted by type (static, param, catch-all) and then by path string for predictable matching.
	nodeType  nodeType               // The type of this node (staticNode, paramNode, catchAllNode).
	paramName string                 // If nodeType is paramNode or catchAllNode, this stores the name of the parameter (e.g., "id", "filepath").
	handlers  map[string]routeTarget // Map of HTTP method (e.g., "GET", "POST") to its `routeTarget` (handler and middleware).
}

// Tree is the radix tree implementation used for Xylium's routing.
// It allows for efficient matching of URL paths to handlers, supporting
// static paths, path parameters, and catch-all routes.
type Tree struct {
	root *node // The root node of the tree, effectively representing the base "/" path.
}

// NewTree creates a new, empty radix tree, initializing the root node.
func NewTree() *Tree {
	return &Tree{
		// Initialize the root node. Its path is effectively empty as it's the conceptual base.
		// It's a static node by nature.
		root: &node{path: "", nodeType: staticNode, children: make([]*node, 0)},
	}
}

// Add registers a new route (handler and middlewares) for a given HTTP method and path pattern.
// - `method`: The HTTP method (e.g., "GET", "POST"), normalized to uppercase.
// - `path`: The URL path pattern (e.g., "/users", "/users/:id", "/files/*filepath").
//           Must begin with "/". Trailing slashes (except for the root path "/") are generally removed.
// - `handler`: The `HandlerFunc` to execute when this route is matched.
// - `middlewares`: A variadic slice of `Middleware` specific to this route.
// Panics if the path format is invalid, if the handler is nil, or if a handler is already registered
// for the same method and path.
func (t *Tree) Add(method, path string, handler HandlerFunc, middlewares ...Middleware) {
	if path == "" || path[0] != '/' {
		panic("xylium: path must begin with '/' (e.g., \"/users\", \"/\")")
	}
	if handler == nil {
		panic("xylium: handler cannot be nil for Add operation")
	}
	method = strings.ToUpper(method) // Normalize HTTP method to uppercase for consistent map keys.

	currentNode := t.root // Start traversal from the root.

	// Normalize path: remove trailing slash if it's not the root path itself.
	// e.g., "/users/" becomes "/users", but "/" remains "/".
	if len(path) > 1 && path[len(path)-1] == '/' {
		path = path[:len(path)-1]
	}

	// Split the path into segments (e.g., "/users/:id" -> ["users", ":id"]).
	// An empty path or "/" results in an empty segments slice if `splitPathOptimized` considers "/" as no segments.
	// Current `splitPathOptimized` will return `[]string{}` for `/`.
	segments := splitPathOptimized(path)

	// Traverse or build the tree based on path segments.
	for i, segment := range segments {
		childNode := currentNode.findOrAddChild(segment) // Find existing child or create a new one.
		currentNode = childNode                          // Move to the child node.

		// Validate catch-all segment placement: it must be the last segment.
		if childNode.nodeType == catchAllNode && i < len(segments)-1 {
			panic(fmt.Sprintf("xylium: catch-all segment '*' must be the last part of the path pattern (e.g. /files/*filepath), offending path: %s", path))
		}
	}

	// At the target node (end of the path), register the handler for the given method.
	if currentNode.handlers == nil {
		currentNode.handlers = make(map[string]routeTarget)
	}
	if _, exists := currentNode.handlers[method]; exists {
		// Prevent duplicate registration for the same method and path.
		panic(fmt.Sprintf("xylium: handler already registered for method %s and path %s", method, path))
	}
	currentNode.handlers[method] = routeTarget{handler: handler, middleware: middlewares}
}

// findOrAddChild attempts to find a child node matching the given segment.
// If no such child exists, it creates a new one, adds it to the parent's children,
// and re-sorts the children to maintain matching priority (static > param > catch-all).
func (n *node) findOrAddChild(segment string) *node {
	nt, paramName := getNodeTypeAndParam(segment) // Determine node type and param name from segment.

	// Try to find an existing child that matches the segment and type.
	for _, child := range n.children {
		if child.nodeType == nt {
			// For static nodes, paths must match exactly.
			// For param/catch-all, the segment string itself (e.g., ":id", "*filepath") is the 'path'.
			if child.path == segment { // This covers static, param, and catch-all by their definition.
				return child
			}
		}
	}

	// No existing child found, create a new one.
	newNode := &node{
		path:      segment,    // Store the raw segment (e.g., "users", ":id", "*filepath").
		nodeType:  nt,
		paramName: paramName,
		children:  make([]*node, 0), // Initialize children slice for the new node.
	}
	n.children = append(n.children, newNode) // Add to parent's children.

	// Re-sort children to maintain matching priority:
	// 1. By nodeType (static < param < catchAll).
	// 2. For same nodeType, by path string (lexicographical, primarily for consistent behavior).
	sort.Slice(n.children, func(i, j int) bool {
		if n.children[i].nodeType != n.children[j].nodeType {
			return n.children[i].nodeType < n.children[j].nodeType // Lower nodeType value = higher priority.
		}
		// If types are the same, sort by path string for deterministic behavior.
		return n.children[i].path < n.children[j].path
	})
	return newNode
}

// Find searches for a handler matching the request's HTTP method and path.
// It traverses the radix tree based on the request path segments.
// Returns:
// - `handler`: The `HandlerFunc` if a match is found for the method and path.
// - `routeMw`: The slice of `Middleware` specific to the matched route.
// - `params`: A map of extracted path parameters (e.g., {"id": "123"}).
// - `allowedMethods`: A sorted slice of HTTP methods that *are* defined for the matched path,
//   even if the requested method itself wasn't found. This is used for 405 Method Not Allowed responses.
// If no route matches the path at all, all return values will be nil/empty.
// If a path matches but not the method, handler/routeMw will be nil, but params and allowedMethods will be populated.
func (t *Tree) Find(method, requestPath string) (handler HandlerFunc, routeMw []Middleware, params map[string]string, allowedMethods []string) {
	currentNode := t.root
	foundParams := make(map[string]string) // Initialize map for path parameters.
	method = strings.ToUpper(method)       // Normalize request method.

	// Normalize requestPath: remove trailing slash if not root.
	if len(requestPath) > 1 && requestPath[len(requestPath)-1] == '/' {
		requestPath = requestPath[:len(requestPath)-1]
	}
	segments := splitPathOptimized(requestPath) // Split request path into segments.

	var matchedNode *node // Pointer to store the node that ultimately matches the path.
	// Recursively search the tree for a node matching the path segments.
	searchPathRecursive(currentNode, segments, 0, foundParams, &matchedNode)

	// If no node matched the full path, or if the matched node has no handlers defined at all.
	if matchedNode == nil || matchedNode.handlers == nil {
		return nil, nil, nil, nil // 404 Not Found scenario from tree's perspective.
	}

	// A node matching the path was found. Collect all methods defined for this node.
	// This list is used for the "Allow" header in 405 responses.
	allowed := make([]string, 0, len(matchedNode.handlers))
	for m := range matchedNode.handlers {
		allowed = append(allowed, m)
	}
	sort.Strings(allowed) // Sort for consistent "Allow" header.

	// Check if a handler exists for the specific requested HTTP method.
	if target, ok := matchedNode.handlers[method]; ok {
		// Handler found for the method and path.
		return target.handler, target.middleware, foundParams, allowed
	}

	// Path matched, but no handler for the requested method (405 Method Not Allowed scenario).
	// Return params and allowedMethods, but nil handler/middleware.
	return nil, nil, foundParams, allowed
}

// searchPathRecursive is the core recursive helper function for `Tree.Find`.
// It attempts to match segments of the request path against nodes in the tree.
// - `current`: The current node being examined in the tree.
// - `segments`: The slice of path segments from the request URL.
// - `segIdx`: The index of the current segment being matched.
// - `params`: The map to store extracted path parameters.
// - `matchedNode`: A pointer to a variable where the successfully matched node will be stored.
// The function explores children based on priority: static, then param, then catch-all.
func searchPathRecursive(current *node, segments []string, segIdx int, params map[string]string, matchedNode **node) {
	// Base case: All segments of the request path have been processed.
	if segIdx == len(segments) {
		// If the current node has handlers defined, it's a potential match.
		if current.handlers != nil {
			*matchedNode = current // Store this node as the match.
		}
		return // End recursion for this path.
	}

	currentSegment := segments[segIdx] // The current path segment to match.

	// Iterate through children of the current node, respecting matching priority.
	// Children are pre-sorted: static, then param, then catch-all.
	for _, child := range current.children {
		switch child.nodeType {
		case staticNode:
			// Static child: segment must match exactly.
			if child.path == currentSegment {
				searchPathRecursive(child, segments, segIdx+1, params, matchedNode)
				if *matchedNode != nil { return } // If a deeper match was found, propagate it up.
			}
		case paramNode:
			// Parameter child: captures the segment value.
			params[child.paramName] = currentSegment // Store param value.
			searchPathRecursive(child, segments, segIdx+1, params, matchedNode)
			if *matchedNode != nil { return } // If a deeper match, propagate.
			delete(params, child.paramName)    // Backtrack: remove param if this path didn't lead to a full match.
		case catchAllNode:
			// Catch-all child: captures this segment and all remaining segments.
			// It must be the last part of a registered route.
			params[child.paramName] = strings.Join(segments[segIdx:], "/")
			if child.handlers != nil { // A catch-all node itself can have handlers.
				*matchedNode = child // This is a match.
			}
			return // Catch-all consumes the rest; no further recursion needed on this branch.
		}
		// If a match (especially a catch-all from a sibling, though catch-alls are last in sorted children)
		// is found and has handlers, we often return early.
		if *matchedNode != nil {
			// If a specific handler was found deeper in a static/param branch, return.
			// If a catch-all on a sibling was hit (which it wouldn't be due to sort order),
			// this check would also apply.
			// This helps to ensure that the first valid handler found along a path is taken.
			return
		}
	}
}

// splitPathOptimized splits a URL path into its constituent segments.
// It handles leading/trailing slashes and empty paths efficiently.
// Example: "/" -> [], "/users" -> ["users"], "/users/:id" -> ["users", ":id"].
func splitPathOptimized(path string) []string {
	if path == "" || path == "/" { // Root path or empty path has no segments according to this logic.
		return []string{}
	}

	// Normalize: remove leading slash for splitting. Trailing slashes should already be handled
	// by `Add` and `Find` before calling this.
	start := 0
	if path[0] == '/' {
		start = 1
	}
	// Path was already normalized for trailing slashes in Tree.Add and Tree.Find,
	// but being defensive here doesn't hurt if called from elsewhere.
	end := len(path)
	if end > start && path[end-1] == '/' { // This check is mostly redundant if called from Add/Find.
		end--
	}

	// If, after trimming, the path is empty (e.g., was just "/" or "//"), no segments.
	if start >= end {
		return []string{}
	}

	trimmedPathView := path[start:end] // View of the path without leading/trailing slashes.

	// Using strings.Split is generally efficient enough and simpler than manual counting/slicing.
	// It correctly handles multiple slashes between segments (e.g., "/foo//bar" -> ["foo", "", "bar"]),
	// though Xylium's path normalization in Add/Find should prevent such inputs to splitPathOptimized.
	// If path could contain empty segments due to "//", strings.Split is appropriate.
	// If paths are guaranteed to be clean (no "//"), manual split might be slightly faster but more complex.
	// Given that paths are generally normalized by the router, `strings.Split` is a robust choice.
	segments := strings.Split(trimmedPathView, "/")

	// Filter out empty strings that might result from strings.Split if the path was like "/foo/"
	// and the trailing slash wasn't removed before split. However, Add/Find already normalize this.
	// This is more of a safeguard.
	// If `trimmedPathView` is "foo/bar", `segments` is ["foo", "bar"].
	// If `trimmedPathView` is "foo", `segments` is ["foo"].
	// If paths are guaranteed to not have leading/trailing slashes for the `trimmedPathView`
	// and no `//` inside, then no empty segments will be produced by `strings.Split`.
	// The current `Add` and `Find` normalize paths well.
	return segments
}

// getNodeTypeAndParam determines the node type (static, param, catch-all)
// and extracts the parameter name from a path segment string.
// Example: "users" -> (staticNode, ""), ":id" -> (paramNode, "id"), "*filepath" -> (catchAllNode, "filepath").
// Panics if parameter/catch-all segments are malformed (e.g., ":", "*").
func getNodeTypeAndParam(segment string) (nodeType, string) {
	if len(segment) == 0 { // Should not happen with `splitPathOptimized` if path is not just "/".
		return staticNode, ""
	}
	switch segment[0] {
	case ':': // Parameter node.
		if len(segment) > 1 {
			return paramNode, segment[1:] // Name is string after ':'.
		}
		panic(fmt.Sprintf("xylium: invalid parameter segment: '%s' (name missing)", segment))
	case '*': // Catch-all node.
		if len(segment) > 1 {
			return catchAllNode, segment[1:] // Name is string after '*'.
		}
		panic(fmt.Sprintf("xylium: invalid catch-all segment: '%s' (name missing)", segment))
	}
	return staticNode, "" // Default to static node.
}

// PrintRoutes logs all registered routes in the tree to the provided `xylium.Logger`.
// This is primarily a debugging utility, typically called when the server starts
// in `DebugMode` to provide visibility into the configured routing table.
// Routes are printed with their HTTP method and full path.
func (t *Tree) PrintRoutes(logger Logger) {
	if logger == nil {
		// Fallback if no logger is provided (should not happen in normal Xylium operation).
		fmt.Println("[XYLIUM-TREE] PrintRoutes: Logger is nil, cannot print routes.")
		return
	}
	// Log a header message at Debug level, as route printing is a debug activity.
	logger.Debugf("Xylium Registered Routes (Radix Tree Structure):")
	// Start recursive printing from the root node with an initial prefix suitable for root children.
	// The root itself having path "" means its children paths start directly with "/segment".
	t.printNodeRoutesRecursive(logger, t.root, "")
}

// printNodeRoutesRecursive is a helper function to recursively traverse the tree
// and log routes using the provided `xylium.Logger`.
// It reconstructs the full path for display purposes.
// - `logger`: The `xylium.Logger` to use for output.
// - `n`: The current `node` being processed.
// - `basePath`: The accumulated path from the root to the parent of `n`.
//               For direct children of root, `basePath` will be "".
func (t *Tree) printNodeRoutesRecursive(logger Logger, n *node, basePath string) {
	// Construct the full path for the current node.
	var currentFullPath string
	if n.path == "" && basePath == "" { // This is the root node itself.
		currentFullPath = "/"
	} else {
		// For non-root nodes or root node's segment (if root had a path, which it doesn't by default).
		// Node path already contains its identifying segment (e.g., "users", ":id").
		// Prepend with basePath and a slash if basePath is not empty.
		currentFullPath = basePath + "/" + n.path
	}
	// Clean up: Remove any leading/trailing slashes if path is not just "/"
	// and ensure no double slashes.
	// Example: if basePath is "" and n.path is "users", currentFullPath becomes "/users".
	// Example: if basePath is "/api" and n.path is "users", currentFullPath becomes "/api/users".
	// Example: if basePath is "/api" and n.path is "", currentFullPath becomes "/api/" (undesired if n.path is for root of group).
	// The n.path for actual segments should not be empty. Root node has n.path = "".

    // Simpler path construction:
    // If current node is root (its path is empty), display path is "/".
    // Otherwise, it's basePath + "/" + node's path.
    if n.path == "" { // True only for the root node.
        currentFullPath = "/"
    } else {
        if basePath == "/" { // If parent was root, child is /segment
            currentFullPath = basePath + n.path
        } else { // If parent was /api, child is /api/segment
            currentFullPath = basePath + "/" + n.path
        }
    }


	// If the current node has handlers, log them.
	if len(n.handlers) > 0 {
		// Sort HTTP methods for consistent and readable output.
		methods := make([]string, 0, len(n.handlers))
		for method := range n.handlers {
			methods = append(methods, method)
		}
		sort.Strings(methods) // Sort alphabetically (DELETE, GET, POST, ...).

		for _, method := range methods {
			// Log the route at Debug level. Use fixed-width for method alignment for readability.
			// Example: "  GET     /users/:id"
			logger.Debugf("  %-7s %s", method, currentFullPath)
		}
	}

	// Recursively call for child nodes.
	// The `currentFullPath` of the current node becomes the `basePath` for its children.
	for _, child := range n.children {
		t.printNodeRoutesRecursive(logger, child, currentFullPath)
	}
}
