// src/xylium/tree.go
package xylium

import (
	"fmt"     // For formatting error messages and route printing
	"sort"    // For sorting child nodes and methods for consistent behavior/output
	"strings" // For path manipulation
)

// nodeType defines the type of a node in the radix tree.
type nodeType uint8

const (
	staticNode   nodeType = iota // Node for static path segments (e.g., /users)
	paramNode                    // Node for path parameters (e.g., /users/:id)
	catchAllNode                 // Node for catch-all parameters (e.g., /static/*filepath)
)

// routeTarget holds the handler and middleware for a specific route and HTTP method.
type routeTarget struct {
	handler    HandlerFunc  // The main request handler: func(*Context) error
	middleware []Middleware // Middleware specific to this particular route
}

// node represents a node in the radix tree.
type node struct {
	path      string                 // The path segment this node represents (e.g., "users", ":id", "*filepath")
	children  []*node                // Child nodes, sorted by type and then path for predictable matching
	nodeType  nodeType               // Type of the node (staticNode, paramNode, catchAllNode)
	paramName string                 // Name of the parameter if nodeType is paramNode or catchAllNode (e.g., "id", "filepath")
	handlers  map[string]routeTarget // Map of HTTP method (e.g., "GET") to its routeTarget (handler and middleware)
}

// Tree is the radix tree implementation used for Xylium's routing.
type Tree struct {
	root *node // The root node of the tree, representing the "/" path base.
}

// NewTree creates a new, empty radix tree.
func NewTree() *Tree {
	return &Tree{
		// Initialize the root node. Its path is effectively empty as it's the base.
		root: &node{path: "", nodeType: staticNode, children: make([]*node, 0)},
	}
}

// Add registers a new route (handler and middlewares) for a given HTTP method and path pattern.
// It panics if the path is invalid, the handler is nil, or if a handler is already registered
// for the same method and path combination.
func (t *Tree) Add(method, path string, handler HandlerFunc, middlewares ...Middleware) {
	if path == "" || path[0] != '/' {
		panic("xylium: path must begin with '/'")
	}
	if handler == nil {
		panic("xylium: handler cannot be nil")
	}
	method = strings.ToUpper(method) // Normalize HTTP method to uppercase.

	currentNode := t.root
	// Normalize path: remove trailing slash if it's not the root path itself.
	if len(path) > 1 && path[len(path)-1] == '/' {
		path = path[:len(path)-1]
	}
	segments := splitPathOptimized(path) // Use the optimized path splitter.

	for i, segment := range segments {
		childNode := currentNode.findOrAddChild(segment)
		currentNode = childNode
		// A catch-all segment must be the last part of the path pattern.
		if childNode.nodeType == catchAllNode && i < len(segments)-1 {
			panic(fmt.Sprintf("xylium: catch-all segment '*' must be the last part of the path pattern (e.g. /files/*filepath), offending path: %s", path))
		}
	}

	// Initialize handlers map if it's nil for the target node.
	if currentNode.handlers == nil {
		currentNode.handlers = make(map[string]routeTarget)
	}
	// Check if a handler for this method and path already exists.
	if _, exists := currentNode.handlers[method]; exists {
		panic(fmt.Sprintf("xylium: handler already registered for method %s and path %s", method, path))
	}
	// Store the handler and its associated middleware.
	currentNode.handlers[method] = routeTarget{handler: handler, middleware: middlewares}
}

// findOrAddChild finds a child node matching the given segment or creates a new one if not found.
// Child nodes are kept sorted to ensure correct matching priority: static > param > catchAll.
func (n *node) findOrAddChild(segment string) *node {
	nt, paramName := getNodeTypeAndParam(segment)

	// Attempt to find an existing child node that matches the segment and type.
	for _, child := range n.children {
		if child.nodeType == nt { // Must match node type.
			if nt == staticNode && child.path == segment { // For static nodes, path segment must match.
				return child
			}
			// For param/catchAll, the raw segment (e.g., ":id") is stored in child.path.
			if (nt == paramNode || nt == catchAllNode) && child.path == segment {
				return child
			}
		}
	}

	// If no matching child is found, create a new one.
	newNode := &node{
		path:      segment,   // Store the raw segment (e.g., "users", ":id").
		nodeType:  nt,
		paramName: paramName,
		children:  make([]*node, 0), // Initialize children slice.
	}
	n.children = append(n.children, newNode)

	// Sort children to ensure correct matching order: static > param > catchAll.
	// This is crucial for routing to work correctly when ambiguous paths exist
	// (e.g., /users/new vs /users/:id).
	sort.Slice(n.children, func(i, j int) bool {
		if n.children[i].nodeType != n.children[j].nodeType {
			return n.children[i].nodeType < n.children[j].nodeType // Order: static, param, catchAll
		}
		// Within the same node type, sort by path for deterministic behavior.
		return n.children[i].path < n.children[j].path
	})
	return newNode
}

// Find searches for a handler matching the request's HTTP method and path.
// It returns:
// - The found HandlerFunc.
// - Route-specific middleware associated with the found route.
// - A map of extracted path parameters.
// - A slice of HTTP methods allowed for the matched path (used for 405 Method Not Allowed).
func (t *Tree) Find(method, requestPath string) (handler HandlerFunc, routeMw []Middleware, params map[string]string, allowedMethods []string) {
	currentNode := t.root
	foundParams := make(map[string]string) // To store extracted parameters.
	method = strings.ToUpper(method)       // Normalize method.

	// Normalize requestPath: remove trailing slash if not the root path.
	if len(requestPath) > 1 && requestPath[len(requestPath)-1] == '/' {
		requestPath = requestPath[:len(requestPath)-1]
	}
	segments := splitPathOptimized(requestPath) // Use the optimized path splitter.

	var matchedNode *node
	// Recursively search the path starting from the root node and the first segment.
	searchPathRecursive(currentNode, segments, 0, foundParams, &matchedNode)

	if matchedNode == nil || matchedNode.handlers == nil {
		// No node matched the path, or the matched node has no handlers defined.
		return nil, nil, nil, nil
	}

	// Collect all HTTP methods allowed for this specific path.
	allowed := make([]string, 0, len(matchedNode.handlers))
	for m := range matchedNode.handlers {
		allowed = append(allowed, m)
	}
	sort.Strings(allowed) // Sort for a consistent "Allow" header in 405 responses.

	// Check if a handler exists for the requested HTTP method.
	if target, ok := matchedNode.handlers[method]; ok {
		return target.handler, target.middleware, foundParams, allowed
	}

	// Handler for the specific method not found, but the path itself exists.
	// Return foundParams and allowedMethods for a 405 Method Not Allowed response.
	return nil, nil, foundParams, allowed
}

// searchPathRecursive is a helper function to recursively find a matching node in the tree.
// It populates `params` with extracted path parameters and sets `matchedNode` if a
// handler-bearing node is found that matches the full path.
func searchPathRecursive(current *node, segments []string, segIdx int, params map[string]string, matchedNode **node) {
	// Base case: If all path segments have been consumed.
	if segIdx == len(segments) {
		// If the current node has handlers, it's a potential match.
		if current.handlers != nil {
			*matchedNode = current
		}
		return
	}

	currentSegment := segments[segIdx]

	// Iterate over the children of the current node.
	// Children are already sorted by nodeType (static, then param, then catchAll).
	for _, child := range current.children {
		switch child.nodeType {
		case staticNode:
			if child.path == currentSegment { // Static segment match.
				searchPathRecursive(child, segments, segIdx+1, params, matchedNode)
				if *matchedNode != nil { // If a match was found deeper, propagate it up.
					return
				}
			}
		case paramNode:
			// Parameter node matches any segment at this level.
			params[child.paramName] = currentSegment // Store the extracted parameter value.
			searchPathRecursive(child, segments, segIdx+1, params, matchedNode)
			if *matchedNode != nil { // If a match was found deeper, propagate.
				return
			}
			// Backtrack: If no match was found down this param path, remove the param.
			// This is important if there are other sibling nodes (e.g., another static route)
			// that could match if this param path fails.
			delete(params, child.paramName)
		case catchAllNode:
			// Catch-all node consumes all remaining segments.
			// It reconstructs the path from the current segment to the end.
			params[child.paramName] = strings.Join(segments[segIdx:], "/")
			if child.handlers != nil { // A catch-all node itself must have handlers to be a match.
				*matchedNode = child
			}
			return // Catch-all is always terminal for this branch of the search.
		}
		// If a match has been found by any child type and it was terminal (e.g. full match),
		// no need to check other children at this level.
		if *matchedNode != nil && (*matchedNode).nodeType == catchAllNode {
			return
		}
	}
}

// splitPathOptimized splits a URL path into its constituent segments.
// This version aims to reduce allocations compared to the standard strings.Split,
// especially by creating string views (sub-slices of the original path string)
// for segments, thus avoiding new string allocations for each segment's data.
// Example: "/users/info" -> ["users", "info"]
// Example: "/" -> []
func splitPathOptimized(path string) []string {
	if path == "" || path == "/" {
		return []string{} // Return an empty slice for root or empty paths.
	}

	// Manually trim leading and trailing slashes to define the relevant part of the path.
	start := 0
	end := len(path)

	if path[0] == '/' {
		start = 1
	}
	// Check 'end > start' to correctly handle paths like "/" which become empty after this.
	if end > start && path[end-1] == '/' {
		end--
	}

	// If the path becomes empty after trimming (e.g., original was "/" or "//").
	if start >= end {
		return []string{}
	}

	// 'trimmedPathView' is a view into the original 'path' string, no new allocation here.
	trimmedPathView := path[start:end]

	// First pass: count segments to pre-allocate the result slice accurately.
	// This avoids reallocations if append is used on a slice with insufficient capacity.
	segmentCount := 0
	inSegment := false
	for i := 0; i < len(trimmedPathView); i++ {
		if trimmedPathView[i] == '/' {
			if inSegment { // Found the end of a segment.
				segmentCount++
				inSegment = false
			}
		} else {
			if !inSegment { // Found the start of a new segment.
				inSegment = true
			}
		}
	}
	if inSegment { // Account for the last segment if the path doesn't end with '/'.
		segmentCount++
	}

	if segmentCount == 0 { // Handles cases like "///" which become empty after trim.
		return []string{}
	}

	// Pre-allocate the slice for segments.
	segments := make([]string, segmentCount)
	segmentIdx := 0
	currentSegmentStart := -1 // Start of the current segment within trimmedPathView.

	// Second pass: extract segments as string views.
	for i := 0; i < len(trimmedPathView); i++ {
		if trimmedPathView[i] == '/' {
			if currentSegmentStart != -1 { // If we are inside a segment.
				// Extract the segment as a view.
				segments[segmentIdx] = trimmedPathView[currentSegmentStart:i]
				segmentIdx++
				currentSegmentStart = -1 // Reset for the next segment.
			}
		} else {
			if currentSegmentStart == -1 { // Mark the start of a new segment.
				currentSegmentStart = i
			}
		}
	}

	// Add the last segment if it exists.
	if currentSegmentStart != -1 {
		segments[segmentIdx] = trimmedPathView[currentSegmentStart:]
	}

	return segments
}

// getNodeTypeAndParam determines the node type and parameter name from a path segment.
// Example: ":id" -> (paramNode, "id")
// Example: "*filepath" -> (catchAllNode, "filepath")
// Example: "users" -> (staticNode, "")
func getNodeTypeAndParam(segment string) (nodeType, string) {
	if len(segment) == 0 {
		// This case should ideally be avoided by `splitPathOptimized` not producing empty segments.
		// If it happens, treating as static might be a fallback, though it indicates an issue.
		return staticNode, ""
	}
	switch segment[0] {
	case ':': // Parameter node.
		if len(segment) > 1 { // Ensure there's a name after ':'.
			return paramNode, segment[1:]
		}
		// Invalid segment like ":" (colon only).
		panic(fmt.Sprintf("xylium: invalid parameter segment: '%s' (must have a name after ':')", segment))
	case '*': // Catch-all node.
		if len(segment) > 1 { // Ensure there's a name after '*'.
			return catchAllNode, segment[1:]
		}
		// Invalid segment like "*" (asterisk only).
		panic(fmt.Sprintf("xylium: invalid catch-all segment: '%s' (must have a name after '*')", segment))
	}
	// Default to a static node if no special prefix is found.
	return staticNode, ""
}

// PrintRoutes logs all registered routes to the provided logger.
// This is useful for debugging purposes, typically called when the server starts in DebugMode.
func (t *Tree) PrintRoutes(logger Logger) {
	if logger == nil {
		// If no logger is provided, we cannot print the routes.
		// The caller (e.g., router_server.go) is responsible for passing a valid logger.
		return
	}
	logger.Printf("[XYLIUM-DEBUG] Registered Routes:")
	t.printNodeRoutesRecursive(logger, t.root, "")
}

// printNodeRoutesRecursive is a helper function to recursively traverse the tree and log routes.
// It reconstructs the full path for display.
func (t *Tree) printNodeRoutesRecursive(logger Logger, n *node, currentPathPrefix string) {
	// logger is assumed to be non-nil here, as it's checked by the public PrintRoutes method.
	var pathForDisplay string

	if n == t.root {
		// For the root node, the display path is simply "/".
		// The currentPathPrefix is "" at this point.
		pathForDisplay = "/"
	} else {
		// Construct the path by appending the current node's segment to the prefix from its parent.
		if currentPathPrefix == "/" { // Avoid "//segment" if prefix is already "/".
			pathForDisplay = "/" + n.path
		} else {
			// If currentPathPrefix is empty (from root), pathForDisplay becomes "/"+n.path
			// If currentPathPrefix is "/api", pathForDisplay becomes "/api"+"/"+n.path
			pathForDisplay = currentPathPrefix + "/" + n.path
		}
	}
	// Further normalize to remove any accidental double slashes. This is a safeguard.
	// e.g., if pathForDisplay became "/api//v1", it should be "/api/v1".
	// Note: The tree structure should generally prevent this if segments are clean.
	pathForDisplay = strings.ReplaceAll(pathForDisplay, "//", "/")
	if len(pathForDisplay) > 1 && pathForDisplay[0] == '/' && pathForDisplay[1] == '/' {
		pathForDisplay = pathForDisplay[1:] // Handle cases like "//segment" from root
	}


	if len(n.handlers) > 0 {
		// Sort methods for consistent and readable output.
		methods := make([]string, 0, len(n.handlers))
		for method := range n.handlers {
			methods = append(methods, method)
		}
		sort.Strings(methods)

		for _, method := range methods {
			// Log with fixed-width method for alignment.
			// Ensure pathForDisplay is never empty if it has handlers.
			// If it's the root with handlers, pathForDisplay should be "/".
			displayPath := pathForDisplay
			if displayPath == "" && n == t.root { // Special case for root node handler if pathForDisplay ended up empty.
				displayPath = "/"
			} else if displayPath == "" && n != t.root { // Should not happen for non-root nodes with handlers.
				logger.Printf("[XYLIUM-DEBUG-WARN] Empty display path for non-root node with handlers. Node Path: %s, Prefix: %s", n.path, currentPathPrefix)
				displayPath = "???" // Indicate an anomaly
			}

			logger.Printf("[XYLIUM-DEBUG]   %-7s %s", method, displayPath)
		}
	}

	// Recursively call for child nodes.
	// The `pathForDisplay` of the current node becomes the `currentPathPrefix` for its children.
	// If the current node is root and pathForDisplay is "/", use "" as prefix for children to avoid "//"
	prefixForChildren := pathForDisplay
	if n == t.root && pathForDisplay == "/" {
		prefixForChildren = "" // For children of root, prefix is effectively empty for path joining.
	}

	for _, child := range n.children {
		t.printNodeRoutesRecursive(logger, child, prefixForChildren)
	}
}
