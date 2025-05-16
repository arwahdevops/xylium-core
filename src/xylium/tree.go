package xylium

import (
	"fmt"
	"sort"
	"strings"
)

// nodeType defines the type of a node in the radix tree.
type nodeType uint8

const (
	staticNode   nodeType = iota // Node for static path segments
	paramNode                    // Node for path parameters (e.g., /users/:id)
	catchAllNode                 // Node for catch-all parameters (e.g., /static/*filepath)
)

// routeTarget holds the handler and middleware for a specific route and method.
type routeTarget struct {
	handler    HandlerFunc  // The main request handler: func(*Context) error
	middleware []Middleware // Middleware specific to this route
}

// node represents a node in the radix tree.
type node struct {
	path      string                 // The path segment this node represents
	children  []*node                // Child nodes
	nodeType  nodeType               // Type of the node (static, param, catchAll)
	paramName string                 // Name of the parameter if nodeType is paramNode or catchAllNode
	handlers  map[string]routeTarget // Map of HTTP method to its handler and middleware
}

// Tree is the radix tree for routing.
type Tree struct {
	root *node
}

// NewTree creates a new, empty radix tree.
func NewTree() *Tree {
	return &Tree{
		root: &node{path: "", nodeType: staticNode, children: make([]*node, 0)}, // Ensure children is initialized
	}
}

// Add registers a new route (handler and middlewares) for a given HTTP method and path pattern.
// Panics if path is invalid, handler is nil, or if a handler is already registered for the same method and path.
func (t *Tree) Add(method, path string, handler HandlerFunc, middlewares ...Middleware) {
	if path == "" || path[0] != '/' {
		panic("xylium: path must begin with '/'")
	}
	if handler == nil {
		panic("xylium: handler cannot be nil")
	}
	method = strings.ToUpper(method)

	currentNode := t.root
	// Normalize path: remove trailing slash if not root path
	if len(path) > 1 && path[len(path)-1] == '/' {
		path = path[:len(path)-1]
	}
	segments := splitPath(path)

	for i, segment := range segments {
		childNode := currentNode.findOrAddChild(segment)
		currentNode = childNode
		// Catch-all segment must be the last part of the path
		if childNode.nodeType == catchAllNode && i < len(segments)-1 {
			panic("xylium: catch-all segment '*' must be the last part of the path pattern (e.g. /files/*filepath)")
		}
	}

	if currentNode.handlers == nil {
		currentNode.handlers = make(map[string]routeTarget)
	}
	if _, exists := currentNode.handlers[method]; exists {
		// PERBAIKAN: Gunakan fmt.Sprintf
		panic(
			fmt.Sprintf("xylium: handler already registered for method %s and path %s", method, path),
		)
	}
	currentNode.handlers[method] = routeTarget{handler: handler, middleware: middlewares}
}

// findOrAddChild finds a child node matching the segment or creates a new one if not found.
// Child nodes are sorted by type (static, param, catchAll) and then by path for predictable matching.
func (n *node) findOrAddChild(segment string) *node {
	nt, paramName := getNodeTypeAndParam(segment)

	// Try to find an existing child
	for _, child := range n.children {
		// Nodes must match both path (for static) or type (for param/catchAll) and name (for param/catchAll)
		if child.nodeType == nt {
			if nt == staticNode && child.path == segment {
				return child
			}
			if (nt == paramNode || nt == catchAllNode) && child.paramName == paramName {
				// It's possible to have /:param1 and /:param2 at the same level if this check isn't strict.
				// However, typical radix trees for HTTP routers treat all :params at a level as "one type" of node,
				// distinguished by their actual path segment (e.g., :id vs :name, but the tree structure for params
				// often just uses the first encountered param name or a generic indicator).
				// For simplicity and to allow different param names at the same level if needed (though not common for this logic),
				// we stick to the segment for matching path, which for param nodes includes the ':' or '*'.
				// The key is that a segment like ":id" is different from ":token".
				// The original logic was: `if child.path == segment && child.nodeType == nt`
				// This is correct. A :param node's path IS the segment e.g. ":id".
				if child.path == segment { // Path (misalnya ":id") harus sama juga
					return child
				}
			}
		}
	}

	// Create a new child node
	newNode := &node{
		path:      segment,
		nodeType:  nt,
		paramName: paramName,
		children:  make([]*node, 0), // Ensure children is initialized
	}
	n.children = append(n.children, newNode)

	// Sort children to ensure correct matching order: static > param > catchAll
	sort.Slice(n.children, func(i, j int) bool {
		if n.children[i].nodeType != n.children[j].nodeType {
			return n.children[i].nodeType < n.children[j].nodeType // static < param < catchAll
		}
		// Within the same type, sort by path for deterministic behavior (though not strictly required for correctness here)
		return n.children[i].path < n.children[j].path
	})
	return newNode
}

// Find searches for a handler matching the request method and path.
// It returns the handler, route-specific middleware, path parameters, and a list of allowed methods for the path.
func (t *Tree) Find(method, requestPath string) (handler HandlerFunc, routeMw []Middleware, params map[string]string, allowedMethods []string) {
	currentNode := t.root
	foundParams := make(map[string]string)
	method = strings.ToUpper(method)

	// Normalize requestPath: remove trailing slash if not root path
	if len(requestPath) > 1 && requestPath[len(requestPath)-1] == '/' {
		requestPath = requestPath[:len(requestPath)-1]
	}
	segments := splitPath(requestPath)

	var matchedNode *node
	// Start search from root, segment index 0
	searchPath(currentNode, segments, 0, foundParams, &matchedNode)

	if matchedNode == nil || matchedNode.handlers == nil {
		// No node matched or matched node has no handlers
		return nil, nil, nil, nil
	}

	// Collect all allowed methods for this path
	allowed := make([]string, 0, len(matchedNode.handlers))
	for m := range matchedNode.handlers {
		allowed = append(allowed, m)
	}
	sort.Strings(allowed) // For consistent "Allow" header

	// Check if a handler exists for the requested method
	if target, ok := matchedNode.handlers[method]; ok {
		return target.handler, target.middleware, foundParams, allowed
	}

	// Handler for method not found, but path exists (for MethodNotAllowed)
	return nil, nil, foundParams, allowed
}

// searchPath is a recursive helper function to find a matching node in the tree.
// It populates `params` and sets `matchedNode` if a handler-bearing node is found.
func searchPath(current *node, segments []string, segIdx int, params map[string]string, matchedNode **node) {
	// If all segments are consumed, check if the current node has handlers
	if segIdx == len(segments) {
		if current.handlers != nil {
			*matchedNode = current
		}
		return
	}

	currentSegment := segments[segIdx]

	// Iterate over children (sorted by type: static, param, catchAll)
	for _, child := range current.children {
		switch child.nodeType {
		case staticNode:
			if child.path == currentSegment {
				searchPath(child, segments, segIdx+1, params, matchedNode)
				if *matchedNode != nil { // If a match was found deeper, return
					return
				}
			}
		case paramNode:
			// Store original param value if already set (for backtracking in complex scenarios, though less common here)
			originalParamValue, hasParam := params[child.paramName]
			params[child.paramName] = currentSegment
			searchPath(child, segments, segIdx+1, params, matchedNode)
			if *matchedNode != nil { // If a match was found deeper, return
				return
			}
			// Backtrack: restore original param value or delete if it wasn't set before
			if hasParam {
				params[child.paramName] = originalParamValue
			} else {
				delete(params, child.paramName)
			}
		case catchAllNode:
			// Catch-all consumes all remaining segments
			params[child.paramName] = strings.Join(segments[segIdx:], "/")
			if child.handlers != nil { // Catch-all node itself must have handlers
				*matchedNode = child
			}
			return // Catch-all is always terminal for this branch of search
		}
	}
}

// splitPath splits a URL path into its segments.
// Example: "/users/info" -> ["users", "info"]
// Example: "/" -> []
func splitPath(path string) []string {
	trimmedPath := strings.Trim(path, "/")
	if trimmedPath == "" {
		// For root path "/" or empty path "", return empty slice.
		// The root node handles this.
		return []string{}
	}
	return strings.Split(trimmedPath, "/")
}

// getNodeTypeAndParam determines the node type and parameter name from a path segment.
func getNodeTypeAndParam(segment string) (nodeType, string) {
	if len(segment) == 0 {
		return staticNode, "" // Should not happen with proper splitPath
	}
	switch segment[0] {
	case ':': // Parameter node
		if len(segment) > 1 {
			return paramNode, segment[1:]
		}
		// Invalid: ":" alone
	case '*': // Catch-all node
		if len(segment) > 1 {
			return catchAllNode, segment[1:]
		}
		// Invalid: "*" alone
	}
	// Default to static node
	return staticNode, ""
}
