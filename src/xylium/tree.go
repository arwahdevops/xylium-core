package xylium

import (
	"fmt"     // For formatting error messages and route printing.
	"sort"    // For sorting child nodes and methods for consistent behavior/output.
	"strings" // For path manipulation (splitting, joining, replacing).
)

// nodeType defines the kind of a node within the radix tree.
// The node type dictates how a path segment is matched and influences matching priority:
//  1. `staticNode`: Highest priority, matches exact path segments.
//  2. `paramNode`: Medium priority, captures dynamic path segments as named parameters.
//  3. `catchAllNode`: Lowest priority, captures all remaining path segments.
type nodeType uint8

const (
	staticNode   nodeType = iota // Represents a static path segment (e.g., "/users", "/products").
	paramNode                    // Represents a named path parameter (e.g., "/users/:id", "/items/:category").
	catchAllNode                 // Represents a catch-all parameter, must be at the end of a path (e.g., "/static/*filepath").
)

// routeTarget encapsulates the endpoint-specific `HandlerFunc` and its associated `Middleware`
// for a route that is registered for a particular HTTP method on a specific tree `node`.
type routeTarget struct {
	// handler is the main `HandlerFunc` (`func(*Context) error`) to be executed
	// when this route is matched.
	handler HandlerFunc
	// middleware is a slice of `Middleware` functions that are specific to this
	// particular route and HTTP method. They are executed before the `handler`.
	middleware []Middleware
}

// node represents a node in the Xylium radix tree. Each `node` corresponds to a
// segment of a URL path. It can have child nodes representing subsequent path segments
// and can store `routeTarget`s (handler and middleware) for different HTTP methods
// if this node represents the end of a registered route path.
type node struct {
	// path is the literal string segment this node represents (e.g., "users", ":id", "*filepath").
	// For the root node of the tree, this is an empty string.
	path string
	// children is a slice of child nodes. These children are sorted to ensure
	// a deterministic matching order, prioritizing static nodes, then parameter nodes,
	// and finally catch-all nodes. Within the same `nodeType`, they are sorted
	// lexicographically by their `path` string for consistency.
	children []*node
	// nodeType indicates the type of this node (static, parameter, or catch-all).
	nodeType nodeType
	// paramName stores the name of the parameter if this node is of type `paramNode`
	// (e.g., "id" for a path segment like ":id") or `catchAllNode`
	// (e.g., "filepath" for a segment like "*filepath").
	// It is an empty string for `staticNode`.
	paramName string
	// handlers is a map where keys are HTTP method strings (e.g., "GET", "POST", normalized
	// to uppercase) and values are `routeTarget` structs containing the handler
	// and middleware for that method at this path node. This map is nil if no
	// routes terminate at this node.
	handlers map[string]routeTarget
}

// Tree is the radix tree implementation used for Xylium's HTTP request routing.
// It allows for efficient matching of URL paths to their corresponding handlers,
// supporting static paths, named path parameters, and catch-all parameters.
// The tree structure enables prefix-based matching and reduces the number of
// comparisons needed to find a route.
type Tree struct {
	// root is the root node of the radix tree. It conceptually represents the
	// base path "/" of the application. All route registrations and lookups
	// start from this node.
	root *node
}

// NewTree creates and returns a new, empty `Tree` instance.
// It initializes the root node of the tree, which is essential for starting
// route registrations.
func NewTree() *Tree {
	return &Tree{
		// Initialize the root node. Its path is an empty string, representing the
		// conceptual start of all paths. It's a static node by nature.
		// Its children slice is initialized to be empty.
		root: &node{path: "", nodeType: staticNode, children: make([]*node, 0)},
	}
}

// Add registers a new route in the radix tree. This involves creating or traversing
// nodes corresponding to the path segments and associating the `handler` and
// `middlewares` with the final node for the specified HTTP `method`.
//
// Parameters:
//   - `method` (string): The HTTP method (e.g., "GET", "POST"). It will be normalized to uppercase.
//   - `path` (string): The URL path pattern for the route (e.g., "/users", "/users/:id", "/files/*filepath").
//     It must begin with "/". Trailing slashes are generally removed, except for the root path "/".
//   - `handler` (HandlerFunc): The `xylium.HandlerFunc` to execute when this route is matched.
//     It must not be nil.
//   - `middlewares` (...Middleware): An optional variadic slice of `xylium.Middleware` functions
//     that are specific to this route and will be executed before the `handler`.
//
// Panics:
//   - If `path` does not begin with "/".
//   - If `handler` is nil.
//   - If a route with the same `method` and `path` has already been registered.
//   - If a catch-all segment (e.g., `*filepath`) is not the last segment in the `path`.
//   - If a parameter or catch-all segment is malformed (e.g., ":" or "*" without a name).
func (t *Tree) Add(method, path string, handler HandlerFunc, middlewares ...Middleware) {
	if path == "" || path[0] != '/' {
		panic("xylium: path must begin with '/' (e.g., \"/users\", \"/\")")
	}
	if handler == nil {
		panic("xylium: handler cannot be nil for Add operation")
	}
	method = strings.ToUpper(method) // Normalize HTTP method to uppercase for consistent map keys.

	currentNode := t.root // Start traversal from the root node.

	// Normalize the path: remove a trailing slash if it's not the root path itself.
	// For example, "/users/" becomes "/users", but "/" remains "/".
	// This ensures consistency in route matching.
	if len(path) > 1 && path[len(path)-1] == '/' {
		path = path[:len(path)-1]
	}

	// Split the normalized path into segments.
	// For example, "/users/:id" becomes ["users", ":id"]. The root path "/" becomes an empty slice.
	segments := splitPathOptimized(path)

	// Traverse the tree, creating nodes as necessary for each path segment.
	for i, segment := range segments {
		// findOrAddChild finds an existing child matching the segment or creates a new one.
		childNode := currentNode.findOrAddChild(segment)
		currentNode = childNode // Move to the child node for the next segment.

		// Validate catch-all segment placement: it must be the last segment in the path pattern.
		if childNode.nodeType == catchAllNode && i < len(segments)-1 {
			panic(fmt.Sprintf("xylium: catch-all segment '*' must be the last part of the path pattern (e.g. /files/*filepath), offending path: %s", path))
		}
	}

	// At the target node (which represents the end of the full path),
	// register the handler and middleware for the given HTTP method.
	if currentNode.handlers == nil {
		currentNode.handlers = make(map[string]routeTarget)
	}
	// Check for duplicate registration: if a handler already exists for this method and path.
	if _, exists := currentNode.handlers[method]; exists {
		panic(fmt.Sprintf("xylium: handler already registered for method %s and path %s", method, path))
	}
	currentNode.handlers[method] = routeTarget{handler: handler, middleware: middlewares}
}

// findOrAddChild is an internal helper method for a `node`. It attempts to find a
// child node that matches the given `segment` string.
//
// If a matching child node (considering `nodeType` and `path`) already exists, it is returned.
// If no such child exists, a new `node` is created based on the `segment` (determining its
// type and parameter name), added to the parent `n`'s children, and then returned.
// After adding a new child, the parent's `children` slice is re-sorted to maintain
// the correct matching priority (static > param > catch-all, then lexicographical).
//
// Panics if the `segment` represents a malformed parameter or catch-all (e.g., ":" or "*").
func (n *node) findOrAddChild(segment string) *node {
	// Determine the node type and parameter name from the segment string.
	// This will panic for malformed segments like ":" or "*".
	nt, paramName := getNodeTypeAndParam(segment)

	// Try to find an existing child that matches the segment's type and path.
	for _, child := range n.children {
		// Nodes are primarily differentiated by type for matching.
		if child.nodeType == nt {
			// For static nodes, `child.path` (e.g., "users") must match `segment`.
			// For param nodes, `child.path` (e.g., ":id") must match `segment`.
			// For catch-all nodes, `child.path` (e.g., "*filepath") must match `segment`.
			// So, a direct string comparison of `child.path` and `segment` works for all types here.
			if child.path == segment {
				return child // Found an existing matching child.
			}
		}
	}

	// No existing child found that matches. Create a new one.
	newNode := &node{
		path:      segment,          // Store the raw segment string (e.g., "users", ":id", "*filepath").
		nodeType:  nt,               // Set the determined node type.
		paramName: paramName,        // Set the extracted parameter name (empty for static nodes).
		children:  make([]*node, 0), // Initialize an empty slice for children of the new node.
	}
	n.children = append(n.children, newNode) // Add the new node to the parent's children.

	// Re-sort the children slice to maintain matching priority.
	// Priority: 1. By nodeType (static < param < catchAll).
	//           2. For nodes of the same type, by path string (lexicographical) for deterministic behavior.
	sort.Slice(n.children, func(i, j int) bool {
		if n.children[i].nodeType != n.children[j].nodeType {
			// Lower nodeType enum value means higher priority (static=0, param=1, catchAll=2).
			return n.children[i].nodeType < n.children[j].nodeType
		}
		// If node types are the same, sort by their path strings for consistent ordering.
		return n.children[i].path < n.children[j].path
	})
	return newNode
}

// Find searches the radix tree for a handler that matches the given HTTP `method` and `requestPath`.
// It traverses the tree based on segments of the `requestPath`.
//
// Parameters:
//   - `method` (string): The HTTP method of the request (e.g., "GET", "POST"), normalized to uppercase.
//   - `requestPath` (string): The URL path of the request. Trailing slashes are removed if not root.
//
// Returns:
//   - `handler` (HandlerFunc): The `xylium.HandlerFunc` if a route matching both path and method is found. Nil otherwise.
//   - `routeMw` ([]Middleware): The slice of `xylium.Middleware` specific to the matched route. Nil if no handler found.
//   - `params` (map[string]string): A map of path parameters extracted from the `requestPath` if the path structure
//     (potentially with parameters) was matched. Populated even if the method doesn't match (for 405).
//   - `allowedMethods` ([]string): A sorted slice of HTTP methods that *are* defined for the matched path node,
//     regardless of whether the requested `method` itself was found. This is used by the router to set the
//     "Allow" header for HTTP 405 "Method Not Allowed" responses.
//
// Behavior:
//   - If a route perfectly matches the path and method: `handler`, `routeMw`, `params`, and `allowedMethods` are returned.
//   - If a path structure matches but no handler is defined for the requested `method`:
//     `handler` and `routeMw` are nil, but `params` (if any) and `allowedMethods` (for the matched path) are populated.
//     This signals a 405 Method Not Allowed situation.
//   - If no path structure in the tree matches the `requestPath`: all return values are nil/empty.
//     This signals a 404 Not Found situation from the tree's perspective.
func (t *Tree) Find(method, requestPath string) (handler HandlerFunc, routeMw []Middleware, params map[string]string, allowedMethods []string) {
	currentNode := t.root                  // Start search from the root of the tree.
	foundParams := make(map[string]string) // Initialize map to store extracted path parameters.
	method = strings.ToUpper(method)       // Normalize the request method to uppercase.

	// Normalize the requestPath: remove trailing slash if it's not the root path.
	if len(requestPath) > 1 && requestPath[len(requestPath)-1] == '/' {
		requestPath = requestPath[:len(requestPath)-1]
	}
	// Split the normalized request path into segments for traversal.
	segments := splitPathOptimized(requestPath)

	var matchedNode *node // Pointer to store the tree node that matches the full path.
	// Recursively search the tree. `matchedNode` will be updated if a path match is found.
	searchPathRecursive(currentNode, segments, 0, foundParams, &matchedNode)

	// If no node in the tree matched the full request path, or if the matched node
	// has no handlers defined for any method (which shouldn't happen for a valid terminal node).
	if matchedNode == nil || matchedNode.handlers == nil {
		return nil, nil, nil, nil // Signals a 404 Not Found from the tree's perspective.
	}

	// A node matching the path structure was found (`matchedNode`).
	// Collect all HTTP methods for which handlers are defined on this node.
	// This list is used for the "Allow" header in 405 Method Not Allowed responses.
	definedMethodsOnNode := make([]string, 0, len(matchedNode.handlers))
	for m := range matchedNode.handlers {
		definedMethodsOnNode = append(definedMethodsOnNode, m)
	}
	sort.Strings(definedMethodsOnNode) // Sort for a consistent "Allow" header.

	// Check if a handler exists for the specific requested HTTP method on the matched node.
	if target, ok := matchedNode.handlers[method]; ok {
		// Handler found for the requested method and path.
		return target.handler, target.middleware, foundParams, definedMethodsOnNode
	}

	// Path structure matched, but no handler for the specific requested `method`.
	// This is a 405 Method Not Allowed situation.
	// Return the extracted params (if any) and the list of allowed methods for this path.
	// Handler and route-specific middleware are nil.
	return nil, nil, foundParams, definedMethodsOnNode
}

// searchPathRecursive is the core recursive search function used by `Tree.Find`.
// It attempts to match the sequence of `segments` from the request path against the
// nodes in the radix tree, starting from `current` node at `segIdx`.
//
// Parameters:
//   - `current` (*node): The current tree node being examined.
//   - `segments` ([]string): The slice of path segments from the request URL.
//   - `segIdx` (int): The index of the current segment in `segments` being matched.
//   - `params` (map[string]string): The map where extracted path parameter values are stored.
//     This map is passed by reference and modified during traversal.
//   - `matchedNode` (**node): A pointer to a `*node` variable in the caller (`Tree.Find`).
//     If a full path match is found, this variable will be updated to point to the
//     terminal node of that path.
//
// The function explores child nodes based on their pre-sorted priority:
// static nodes first, then parameter nodes, then catch-all nodes.
// If a match is found along a branch, it continues recursively. If a branch does not
// lead to a full match, parameter values captured along that branch are backtracked (removed).
func searchPathRecursive(current *node, segments []string, segIdx int, params map[string]string, matchedNode **node) {
	// Base case for recursion: all segments of the request path have been processed.
	if segIdx == len(segments) {
		// If the current node has handlers defined (i.e., it's a terminal node for some routes),
		// this means the full path has been matched.
		if current.handlers != nil {
			*matchedNode = current // Store this node as the successfully matched terminal node.
		}
		return // End recursion for this particular path.
	}

	currentSegment := segments[segIdx] // The current path segment from the request to match.

	// Iterate through the children of the `current` node.
	// Children are pre-sorted by `findOrAddChild` to ensure correct matching priority:
	// static nodes, then parameter nodes, then catch-all nodes.
	for _, child := range current.children {
		// Try to match based on the child's node type.
		switch child.nodeType {
		case staticNode:
			// For a static child node, the request segment must exactly match the child's path.
			if child.path == currentSegment {
				// Match found. Recurse deeper with the next segment.
				searchPathRecursive(child, segments, segIdx+1, params, matchedNode)
				if *matchedNode != nil {
					// If a full match was found in the deeper recursion (e.g., a handler was set on a descendant),
					// propagate this result up and stop further searching on this level for this branch.
					return
				}
				// If no match was found deeper, this static branch doesn't lead to a handler.
				// Continue to the next child of `current`.
			}
		case paramNode:
			// For a parameter child node, it captures the current request segment as a parameter value.
			params[child.paramName] = currentSegment                            // Store the captured parameter value.
			searchPathRecursive(child, segments, segIdx+1, params, matchedNode) // Recurse deeper.
			if *matchedNode != nil {
				// Full match found in this parameter branch. Propagate up.
				return
			}
			// Backtrack: If this parameter branch didn't lead to a full match,
			// remove the parameter value captured at this step. This is crucial for
			// allowing other sibling branches (e.g., another static path at the same level)
			// to be tried correctly without this param polluting their state.
			delete(params, child.paramName)
		case catchAllNode:
			// For a catch-all child node, it captures the current segment and all
			// remaining segments of the request path.
			// A catch-all node must be the terminal part of a registered route pattern.
			params[child.paramName] = strings.Join(segments[segIdx:], "/") // Join remaining segments.
			// If this catch-all node itself has handlers, it's a match.
			if child.handlers != nil {
				*matchedNode = child
			}
			// A catch-all consumes all remaining segments. No further recursion down this branch
			// for matching *more* segments. Stop searching other children of `current` too,
			// as catch-all has the lowest priority among siblings and if it matches, it's the one.
			return
		}

		// Optimization: If a `matchedNode` was already found (e.g., from a higher priority
		// static path that was tried before a param path at the same level, or a catch-all
		// that matched), we can often stop searching further sibling nodes at this level.
		// However, the current loop structure already handles priority by iterating through
		// sorted children. If a static/param child leads to a `*matchedNode != nil`, the `return`
		// inside those cases will exit. A catch-all also returns. This check might be
		// redundant if the returns inside the switch cases are correctly placed.
		// Let's keep it simple: the returns within the switch cases handle termination.
	}
}

// splitPathOptimized splits a URL path string into its constituent segments.
// It is designed to handle paths normalized by `Tree.Add` and `Tree.Find`
// (i.e., leading slash, no trailing slash unless root "/").
//
// Examples:
//
//	"/"             -> []string{} (empty slice, representing no segments beyond root)
//	"/users"        -> []string{"users"}
//	"/users/:id"    -> []string{"users", ":id"}
//	"/a/b/c"        -> []string{"a", "b", "c"}
//
// The function first removes the leading slash (if present and not the only char)
// before splitting by "/". This correctly handles the root path "/" resulting in
// an empty slice of segments.
func splitPathOptimized(path string) []string {
	if path == "" || path == "/" {
		// The root path or an empty path string is considered to have no segments
		// for the purpose of tree traversal beyond the root node.
		return []string{}
	}

	// Effective path to split: remove the leading slash.
	// E.g., "/users" becomes "users", "/users/:id" becomes "users/:id".
	// `path[1:]` is safe because `path` is guaranteed not to be empty and to start with `/`
	// due to checks in `Tree.Add` (or it's already handled if path == "/").
	// Trailing slashes should have been removed by `Tree.Add` or `Tree.Find` prior to this call.
	effectivePath := path[1:]

	// `strings.Split` will split the string by "/".
	// E.g., "users" -> ["users"]
	// E.g., "users/:id" -> ["users", ":id"]
	// E.g., "a/b/c" -> ["a", "b", "c"]
	// If `effectivePath` were empty (e.g., if original path was "/"), `strings.Split`
	// on an empty string yields `[]string{""}` (a slice with one empty string).
	// This is why the `path == "/"` case is handled separately above to return `[]string{}`.
	return strings.Split(effectivePath, "/")
}

// getNodeTypeAndParam analyzes a path segment string to determine its `nodeType`
// (static, parameter, or catch-all) and extracts the parameter name if applicable.
//
// Examples:
//
//	"users"     -> (staticNode, "")
//	":id"       -> (paramNode, "id")
//	"*filepath" -> (catchAllNode, "filepath")
//
// Panics:
//   - If the segment is a malformed parameter (e.g., ":" without a name).
//   - If the segment is a malformed catch-all (e.g., "*" without a name).
//   - An empty segment string results in (staticNode, "").
func getNodeTypeAndParam(segment string) (nodeType, string) {
	if len(segment) == 0 {
		// An empty segment (e.g., from splitting "//") is treated as a static node
		// with an empty path. This scenario should ideally be avoided by path normalization
		// before splitting, but if it occurs, this is how it's handled.
		return staticNode, ""
	}
	switch segment[0] {
	case ':': // Indicates a parameter node.
		if len(segment) > 1 { // Must have a name after ':'.
			return paramNode, segment[1:] // The name is the string part after ':'.
		}
		// Malformed parameter: ":" with no name.
		panic(fmt.Sprintf("xylium: invalid parameter segment: '%s' (parameter name missing after ':')", segment))
	case '*': // Indicates a catch-all node.
		if len(segment) > 1 { // Must have a name after '*'.
			return catchAllNode, segment[1:] // The name is the string part after '*'.
		}
		// Malformed catch-all: "*" with no name.
		panic(fmt.Sprintf("xylium: invalid catch-all segment: '%s' (parameter name missing after '*')", segment))
	}
	// If not starting with ':' or '*', it's a static node. Parameter name is empty.
	return staticNode, ""
}

// PrintRoutes logs all registered routes in the radix tree to the provided `xylium.Logger`.
// This function is primarily a debugging utility, often called when the server starts
// in `DebugMode` to provide a clear overview of the application's routing table.
//
// Routes are printed with their HTTP method (left-aligned) and the full path pattern.
// The output is structured to reflect the tree hierarchy, although this version prints
// each full path directly.
//
// If `logger` is nil, a message is printed to standard output indicating that routes
// cannot be printed.
func (t *Tree) PrintRoutes(logger Logger) {
	if logger == nil {
		// Fallback if no logger is provided. This should ideally not happen
		// in normal Xylium operation as the router always has a logger.
		fmt.Println("[XYLIUM-TREE-PRINT] PrintRoutes: Logger is nil, cannot print routes.")
		return
	}
	// Log a header message at Debug level, as route printing is typically for debugging.
	logger.Debugf("Xylium Registered Routes (Radix Tree Structure):")
	// Start the recursive printing process from the root node.
	// The initial `basePath` for children of the root is effectively "/".
	// However, `printNodeRoutesRecursive` handles path construction carefully.
	t.printNodeRoutesRecursive(logger, t.root, "")
}

// printNodeRoutesRecursive is an internal helper function to recursively traverse the
// radix tree and log the registered routes using the provided `xylium.Logger`.
// It reconstructs the full path for each route for display purposes.
//
// Parameters:
//   - `logger` (Logger): The `xylium.Logger` instance to use for output.
//   - `n` (*node): The current tree `node` being processed.
//   - `basePath` (string): The accumulated path string from the root of the tree
//     down to the parent of the current node `n`. For children of the root,
//     `basePath` will initially be an empty string, which is handled to form "/".
func (t *Tree) printNodeRoutesRecursive(logger Logger, n *node, basePath string) {
	// Construct the full path for the current node `n`.
	var currentFullPath string
	if n.path == "" { // This condition is typically true only for the absolute root node.
		currentFullPath = "/"
		if basePath != "" { // If basePath is not empty, means root is part of a group like /api
			currentFullPath = basePath // Then the group path is the "current full path" for root's handlers
		}
	} else {
		// For non-root nodes, or segments under a group.
		if basePath == "/" || basePath == "" { // If parent was root or effectively root for printing.
			currentFullPath = "/" + n.path
		} else {
			currentFullPath = basePath + "/" + n.path
		}
	}

	// If the current node `n` has any handlers registered (i.e., routes terminate here).
	if len(n.handlers) > 0 {
		// Sort the HTTP methods for consistent and readable output.
		methods := make([]string, 0, len(n.handlers))
		for method := range n.handlers {
			methods = append(methods, method)
		}
		sort.Strings(methods) // Sort alphabetically (e.g., DELETE, GET, POST).

		// Log each registered method and its full path.
		for _, method := range methods {
			// Log at Debug level. Use fixed-width formatting for the method for alignment.
			// Example output: "  GET     /users/:id"
			logger.Debugf("  %-7s %s", method, currentFullPath)
		}
	}

	// Recursively call `printNodeRoutesRecursive` for all children of the current node.
	// The `currentFullPath` of this node becomes the `basePath` for its children.
	for _, child := range n.children {
		t.printNodeRoutesRecursive(logger, child, currentFullPath)
	}
}
