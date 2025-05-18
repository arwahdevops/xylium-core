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
// (No changes needed in Add itself regarding logging)
func (t *Tree) Add(method, path string, handler HandlerFunc, middlewares ...Middleware) {
	if path == "" || path[0] != '/' {
		panic("xylium: path must begin with '/'")
	}
	if handler == nil {
		panic("xylium: handler cannot be nil")
	}
	method = strings.ToUpper(method) // Normalize HTTP method to uppercase.

	currentNode := t.root
	if len(path) > 1 && path[len(path)-1] == '/' {
		path = path[:len(path)-1]
	}
	segments := splitPathOptimized(path)

	for i, segment := range segments {
		childNode := currentNode.findOrAddChild(segment)
		currentNode = childNode
		if childNode.nodeType == catchAllNode && i < len(segments)-1 {
			panic(fmt.Sprintf("xylium: catch-all segment '*' must be the last part of the path pattern (e.g. /files/*filepath), offending path: %s", path))
		}
	}

	if currentNode.handlers == nil {
		currentNode.handlers = make(map[string]routeTarget)
	}
	if _, exists := currentNode.handlers[method]; exists {
		panic(fmt.Sprintf("xylium: handler already registered for method %s and path %s", method, path))
	}
	currentNode.handlers[method] = routeTarget{handler: handler, middleware: middlewares}
}

// findOrAddChild finds a child node matching the given segment or creates a new one if not found.
// (No changes needed here)
func (n *node) findOrAddChild(segment string) *node {
	nt, paramName := getNodeTypeAndParam(segment)

	for _, child := range n.children {
		if child.nodeType == nt {
			if nt == staticNode && child.path == segment {
				return child
			}
			if (nt == paramNode || nt == catchAllNode) && child.path == segment {
				return child
			}
		}
	}

	newNode := &node{
		path:      segment,
		nodeType:  nt,
		paramName: paramName,
		children:  make([]*node, 0),
	}
	n.children = append(n.children, newNode)

	sort.Slice(n.children, func(i, j int) bool {
		if n.children[i].nodeType != n.children[j].nodeType {
			return n.children[i].nodeType < n.children[j].nodeType
		}
		return n.children[i].path < n.children[j].path
	})
	return newNode
}

// Find searches for a handler matching the request's HTTP method and path.
// (No changes needed here)
func (t *Tree) Find(method, requestPath string) (handler HandlerFunc, routeMw []Middleware, params map[string]string, allowedMethods []string) {
	currentNode := t.root
	foundParams := make(map[string]string)
	method = strings.ToUpper(method)

	if len(requestPath) > 1 && requestPath[len(requestPath)-1] == '/' {
		requestPath = requestPath[:len(requestPath)-1]
	}
	segments := splitPathOptimized(requestPath)

	var matchedNode *node
	searchPathRecursive(currentNode, segments, 0, foundParams, &matchedNode)

	if matchedNode == nil || matchedNode.handlers == nil {
		return nil, nil, nil, nil
	}

	allowed := make([]string, 0, len(matchedNode.handlers))
	for m := range matchedNode.handlers {
		allowed = append(allowed, m)
	}
	sort.Strings(allowed)

	if target, ok := matchedNode.handlers[method]; ok {
		return target.handler, target.middleware, foundParams, allowed
	}
	return nil, nil, foundParams, allowed
}

// searchPathRecursive is a helper function to recursively find a matching node in the tree.
// (No changes needed here)
func searchPathRecursive(current *node, segments []string, segIdx int, params map[string]string, matchedNode **node) {
	if segIdx == len(segments) {
		if current.handlers != nil {
			*matchedNode = current
		}
		return
	}
	currentSegment := segments[segIdx]
	for _, child := range current.children {
		switch child.nodeType {
		case staticNode:
			if child.path == currentSegment {
				searchPathRecursive(child, segments, segIdx+1, params, matchedNode)
				if *matchedNode != nil { return }
			}
		case paramNode:
			params[child.paramName] = currentSegment
			searchPathRecursive(child, segments, segIdx+1, params, matchedNode)
			if *matchedNode != nil { return }
			delete(params, child.paramName)
		case catchAllNode:
			params[child.paramName] = strings.Join(segments[segIdx:], "/")
			if child.handlers != nil {
				*matchedNode = child
			}
			return
		}
		if *matchedNode != nil && (*matchedNode).nodeType == catchAllNode {
			return
		}
	}
}

// splitPathOptimized splits a URL path into its constituent segments.
// (No changes needed here)
func splitPathOptimized(path string) []string {
	if path == "" || path == "/" {
		return []string{}
	}
	start := 0
	end := len(path)
	if path[0] == '/' {
		start = 1
	}
	if end > start && path[end-1] == '/' {
		end--
	}
	if start >= end {
		return []string{}
	}
	trimmedPathView := path[start:end]
	segmentCount := 0
	inSegment := false
	for i := 0; i < len(trimmedPathView); i++ {
		if trimmedPathView[i] == '/' {
			if inSegment { segmentCount++; inSegment = false }
		} else {
			if !inSegment { inSegment = true }
		}
	}
	if inSegment { segmentCount++ }
	if segmentCount == 0 { return []string{} }
	segments := make([]string, segmentCount)
	segmentIdx := 0
	currentSegmentStart := -1
	for i := 0; i < len(trimmedPathView); i++ {
		if trimmedPathView[i] == '/' {
			if currentSegmentStart != -1 {
				segments[segmentIdx] = trimmedPathView[currentSegmentStart:i]
				segmentIdx++
				currentSegmentStart = -1
			}
		} else {
			if currentSegmentStart == -1 { currentSegmentStart = i }
		}
	}
	if currentSegmentStart != -1 {
		segments[segmentIdx] = trimmedPathView[currentSegmentStart:]
	}
	return segments
}

// getNodeTypeAndParam determines the node type and parameter name from a path segment.
// (No changes needed here)
func getNodeTypeAndParam(segment string) (nodeType, string) {
	if len(segment) == 0 {
		return staticNode, ""
	}
	switch segment[0] {
	case ':':
		if len(segment) > 1 { return paramNode, segment[1:] }
		panic(fmt.Sprintf("xylium: invalid parameter segment: '%s'", segment))
	case '*':
		if len(segment) > 1 { return catchAllNode, segment[1:] }
		panic(fmt.Sprintf("xylium: invalid catch-all segment: '%s'", segment))
	}
	return staticNode, ""
}

// PrintRoutes logs all registered routes to the provided xylium.Logger.
// This is typically called when the server starts in DebugMode to provide visibility
// into the configured routing table.
func (t *Tree) PrintRoutes(logger Logger) {
	if logger == nil {
		// If no logger is provided, cannot print routes.
		// This check is defensive; router_server.go should always pass a valid logger.
		fmt.Println("Xylium Tree.PrintRoutes: Logger is nil, cannot print routes.") // Fallback to fmt.Println
		return
	}
	// Log the header message at Debug level, as route printing is a debug activity.
	logger.Debugf("Xylium Registered Routes:")
	t.printNodeRoutesRecursive(logger, t.root, "")
}

// printNodeRoutesRecursive is a helper function to recursively traverse the tree
// and log routes using the provided xylium.Logger.
// It reconstructs the full path for display.
func (t *Tree) printNodeRoutesRecursive(logger Logger, n *node, currentPathPrefix string) {
	// Determine the display path for the current node.
	var pathForDisplay string
	if n == t.root {
		// The root node itself represents the "/" path if it has handlers,
		// or it's the base for its children.
		pathForDisplay = "/"
	} else {
		// For non-root nodes, append their segment to the parent's path.
		if currentPathPrefix == "/" { // Avoid double slashes like "//segment".
			pathForDisplay = "/" + n.path
		} else {
			// currentPathPrefix is empty for direct children of root (becomes "/segment").
			// currentPathPrefix is "/api" for children of "/api" (becomes "/api/segment").
			pathForDisplay = currentPathPrefix + "/" + n.path
		}
	}
	// Clean up any accidental double slashes that might have formed.
	// Although the logic above tries to prevent it, this is a good final pass.
	pathForDisplay = strings.ReplaceAll(pathForDisplay, "//", "/")
	// A special case: if path was just "//", it becomes "/", which is correct.
	// If it started like "//segment" due to an empty root path prefix, it becomes "/segment".


	// If the current node has handlers, log them.
	if len(n.handlers) > 0 {
		// Sort HTTP methods for consistent and readable output.
		methods := make([]string, 0, len(n.handlers))
		for method := range n.handlers {
			methods = append(methods, method)
		}
		sort.Strings(methods)

		for _, method := range methods {
			// Ensure displayPath is correct, especially for root node with handlers.
			// If pathForDisplay is "/" and it's the root, it's correct.
			// If somehow pathForDisplay is empty for the root (should not happen with current logic), fix it.
			displayPath := pathForDisplay
			if displayPath == "" && n == t.root { // Should resolve to "/" if root
				displayPath = "/"
			} else if displayPath == "" && n != t.root {
				// This indicates an unexpected state, log a warning.
				logger.Warnf("Xylium Tree.PrintRoutes: Empty display path for non-root node with handlers. Node Path: '%s', Prefix: '%s'", n.path, currentPathPrefix)
				displayPath = "[INVALID_PATH_BUG?]" // Mark anomaly.
			}

			// Log the route at Debug level. Use fixed-width for method alignment.
			logger.Debugf("  %-7s %s", method, displayPath)
		}
	}

	// Recursively call for child nodes.
	// The `pathForDisplay` of the current node becomes the `currentPathPrefix` for its children.
	// However, if the current node is the root and its display path is "/",
	// the prefix for its direct children should be empty to avoid "/user" becoming "//user".
	prefixForChildren := pathForDisplay
	if n == t.root && pathForDisplay == "/" {
		prefixForChildren = "" // Direct children of root start their path from scratch after "/".
	}

	for _, child := range n.children {
		t.printNodeRoutesRecursive(logger, child, prefixForChildren)
	}
}
