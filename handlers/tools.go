package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"github.com/sourcegraph/zoekt"
	"github.com/trondhindenes/code-index-mcp/indexer"
)

var manager *indexer.IndexManager
var remoteSearcher *indexer.RemoteSearcher
var webServerManager *indexer.WebServerManager

func init() {
	// Initialize the index manager with user profile directory
	indexDir := getIndexDirectory()
	manager = indexer.NewIndexManager(indexDir)
	webServerManager = indexer.NewWebServerManager(indexDir)

	// Set up remote searcher if ZOEKT_URL is configured
	if url := os.Getenv("ZOEKT_URL"); url != "" {
		remoteSearcher = indexer.NewRemoteSearcher(url)
	}
}

// getIndexDirectory returns the directory where indexes should be stored
func getIndexDirectory() string {
	// Check for custom index directory from environment
	if dir := os.Getenv("CODE_INDEX_DIR"); dir != "" {
		return dir
	}

	// Use platform-specific user data directory
	homeDir, err := os.UserHomeDir()
	if err != nil {
		// Fallback to current directory
		return ".code-index"
	}

	// Use ~/.local/share/code-index on Unix-like systems
	// or ~/Library/Application Support/code-index on macOS
	// or %APPDATA%/code-index on Windows
	switch {
	case os.Getenv("XDG_DATA_HOME") != "":
		return filepath.Join(os.Getenv("XDG_DATA_HOME"), "code-index")
	case filepath.Separator == '/':
		// Unix-like (Linux, macOS)
		if _, err := os.Stat("/Users"); err == nil {
			// macOS
			return filepath.Join(homeDir, "Library", "Application Support", "code-index")
		}
		return filepath.Join(homeDir, ".local", "share", "code-index")
	default:
		// Windows
		if appData := os.Getenv("APPDATA"); appData != "" {
			return filepath.Join(appData, "code-index")
		}
		return filepath.Join(homeDir, ".code-index")
	}
}

// RegisterTools registers all MCP tools with the server
func RegisterTools(s *server.MCPServer) {
	if remoteSearcher != nil {
		// External mode only exposes tools that are meaningful when backed by ZOEKT_URL.
		searchTool := mcp.NewTool("search_code",
			mcp.WithDescription(`Search code using Zoekt query syntax against the external Zoekt instance configured by ZOEKT_URL.

QUERY SYNTAX:
- Content search: "search_term" or /regex.*pattern/
- Fields: file:pattern, lang:go, repo:name, branch:main, sym:SymbolName
- Negation: -lang:javascript (exclude), -file:*.test.go
- Grouping: (lang:go or lang:rust) with parentheses
- Operators: "or" explicit, "and" implicit (space-separated)

KEY FIELDS:
- file:f (filename pattern), lang:l (language), repo:r (repository)
- case:yes|no|auto (default: auto - sensitive if uppercase present)
- branch:b (specific branch), type:filematch|filename|repo (result type)

EXAMPLES:
- "func main" file:main.go
- lang:go content:/func\s+\w+/
- (repo:org/repo1 or repo:org/repo2) "import"
- case:yes "MyStruct" -file:*_test.go`),
			mcp.WithString("query",
				mcp.Required(),
				mcp.Description(`Zoekt query. Use "text" for literal search or /regex/ for patterns. Combine with fields: file:name, lang:go, case:yes. Use - prefix for exclusion, () for grouping, "or" for alternatives. Examples: "TODO" file:*.go, (lang:go or lang:rust) "error", -file:*_test.go "import"`),
			),
			mcp.WithNumber("max_files",
				mcp.Description("Maximum number of files to return (default: 20)"),
			),
			mcp.WithNumber("max_lines_per_file",
				mcp.Description("Maximum matches to show per file (default: 3)"),
			),
			mcp.WithBoolean("files_only",
				mcp.Description("Only return file paths, no line content (default: false)"),
			),
		)
		s.AddTool(searchTool, handleSearchCode)
		return
	}

	// Index directory tool
	indexTool := mcp.NewTool("index_directory",
		mcp.WithDescription("Index a source code directory for fast searching. Creates a Zoekt index that enables fast code search."),
		mcp.WithString("directory",
			mcp.Required(),
			mcp.Description("The absolute or relative path to the directory to index"),
		),
	)
	s.AddTool(indexTool, handleIndexDirectory)

	// Search tool
	searchTool := mcp.NewTool("search_code",
		mcp.WithDescription(`Search for code across indexed directories using Zoekt query syntax.

QUERY SYNTAX:
- Content search: "search_term" or /regex.*pattern/
- Fields: file:pattern, lang:go, repo:name, branch:main, sym:SymbolName
- Negation: -lang:javascript (exclude), -file:*.test.go
- Grouping: (lang:go or lang:rust) with parentheses
- Operators: "or" explicit, "and" implicit (space-separated)

KEY FIELDS:
- file:f (filename pattern), lang:l (language), repo:r (repository)
- case:yes|no|auto (default: auto - sensitive if uppercase present)
- branch:b (specific branch), type:filematch|filename|repo (result type)

EXAMPLES:
- "func main" file:main.go
- lang:go content:/func\s+\w+/
- (repo:org/repo1 or repo:org/repo2) "import"
- case:yes "MyStruct" -file:*_test.go

Returns compact grep-like output.`),
		mcp.WithString("query",
			mcp.Required(),
			mcp.Description(`Zoekt query. Use "text" for literal search or /regex/ for patterns. Combine with fields: file:name, lang:go, case:yes. Use - prefix for exclusion, () for grouping, "or" for alternatives. Examples: "TODO" file:*.go, (lang:go or lang:rust) "error", -file:*_test.go "import"`),
		),
		mcp.WithString("directory",
			mcp.Description("Optional: limit search to a specific indexed directory path"),
		),
		mcp.WithNumber("max_files",
			mcp.Description("Maximum number of files to return (default: 20)"),
		),
		mcp.WithNumber("max_lines_per_file",
			mcp.Description("Maximum matches to show per file (default: 3)"),
		),
		mcp.WithBoolean("files_only",
			mcp.Description("Only return file paths, no line content (default: false)"),
		),
	)
	s.AddTool(searchTool, handleSearchCode)

	// List indexes tool
	listTool := mcp.NewTool("list_indexes",
		mcp.WithDescription("List all indexed directories and their status"),
	)
	s.AddTool(listTool, handleListIndexes)

	// Delete index tool
	deleteTool := mcp.NewTool("delete_index",
		mcp.WithDescription("Delete the index for a specific directory"),
		mcp.WithString("directory",
			mcp.Required(),
			mcp.Description("The path to the directory whose index should be deleted"),
		),
	)
	s.AddTool(deleteTool, handleDeleteIndex)

	// Get index info tool
	infoTool := mcp.NewTool("index_info",
		mcp.WithDescription("Get information about the indexing configuration, including the index storage location"),
	)
	s.AddTool(infoTool, handleIndexInfo)

	// Start webserver tool
	startWebserverTool := mcp.NewTool("start_webserver",
		mcp.WithDescription("Start the Zoekt web server for interactive code search in a browser. The server runs in the background and provides a web UI for searching indexed code. Port can be configured via CODE_INDEX_WEBSERVER_PORT environment variable (default: 6070)."),
		mcp.WithNumber("port",
			mcp.Description("Port to run the web server on. Overrides CODE_INDEX_WEBSERVER_PORT env var. Use 0 for random available port."),
		),
	)
	s.AddTool(startWebserverTool, handleStartWebserver)

	// Stop webserver tool
	stopWebserverTool := mcp.NewTool("stop_webserver",
		mcp.WithDescription("Stop the running Zoekt web server"),
	)
	s.AddTool(stopWebserverTool, handleStopWebserver)

	// Webserver status tool
	webserverStatusTool := mcp.NewTool("webserver_status",
		mcp.WithDescription("Get the current status of the Zoekt web server"),
	)
	s.AddTool(webserverStatusTool, handleWebserverStatus)
}

func handleIndexDirectory(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	directory, err := request.RequireString("directory")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	if err := manager.IndexDirectory(directory); err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("Failed to index directory: %v", err)), nil
	}

	absPath, _ := filepath.Abs(directory)
	return mcp.NewToolResultText(fmt.Sprintf("Successfully indexed directory: %s\nIndex stored in: %s", absPath, manager.GetIndexDir())), nil
}

func handleSearchCode(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	query, err := request.RequireString("query")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	directory := request.GetString("directory", "")

	opts := indexer.SearchOptions{
		MaxFiles:        int(request.GetFloat("max_files", 20)),
		MaxLinesPerFile: int(request.GetFloat("max_lines_per_file", 3)),
		MaxLineLength:   200,
		FilesOnly:       request.GetBool("files_only", false),
	}

	var result *indexer.SearchResult

	if remoteSearcher != nil {
		zoektOpts := &zoekt.SearchOptions{
			MaxDocDisplayCount: opts.MaxFiles * 2,
		}
		zoektResult, err := remoteSearcher.Search(ctx, query, zoektOpts)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("Search failed: %v", err)), nil
		}
		result = indexer.FormatRemoteSearchResults(zoektResult, opts)
	} else {
		result, err = manager.Search(query, directory, opts)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("Search failed: %v", err)), nil
		}
	}

	if len(result.Lines) == 0 {
		return mcp.NewToolResultText("No results found"), nil
	}

	// Return compact grep-like output
	output := strings.Join(result.Lines, "\n")
	return mcp.NewToolResultText(output), nil
}

func handleListIndexes(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	indexes, err := manager.ListIndexes()
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("Failed to list indexes: %v", err)), nil
	}

	if len(indexes) == 0 {
		return mcp.NewToolResultText("No indexes found. Use 'index_directory' to create an index."), nil
	}

	output, err := json.MarshalIndent(indexes, "", "  ")
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("Failed to format indexes: %v", err)), nil
	}

	return mcp.NewToolResultText(string(output)), nil
}

func handleDeleteIndex(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	directory, err := request.RequireString("directory")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	if err := manager.DeleteIndex(directory); err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("Failed to delete index: %v", err)), nil
	}

	absPath, _ := filepath.Abs(directory)
	return mcp.NewToolResultText(fmt.Sprintf("Successfully deleted index for: %s", absPath)), nil
}

func handleIndexInfo(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	info := map[string]string{
		"index_directory": manager.GetIndexDir(),
		"description":     "All indexes are stored as .zoekt files in the index directory, with unique prefixes per source directory",
	}
	if remoteSearcher != nil {
		info["mode"] = "external"
		info["zoekt_url"] = os.Getenv("ZOEKT_URL")
	}

	output, err := json.MarshalIndent(info, "", "  ")
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("Failed to format info: %v", err)), nil
	}

	return mcp.NewToolResultText(string(output)), nil
}

// getDefaultWebserverPort returns the default port from env or 6070
func getDefaultWebserverPort() int {
	if portStr := os.Getenv("CODE_INDEX_WEBSERVER_PORT"); portStr != "" {
		var port int
		if _, err := fmt.Sscanf(portStr, "%d", &port); err == nil && port > 0 {
			return port
		}
	}
	return 6070
}

func handleStartWebserver(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	if remoteSearcher != nil {
		return mcp.NewToolResultError("Not available in external mode (ZOEKT_URL is set). The webserver is managed externally."), nil
	}
	// Get port from request or use default
	port := int(request.GetFloat("port", float64(getDefaultWebserverPort())))

	status, err := webServerManager.Start(port)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("Failed to start web server: %v", err)), nil
	}

	output, err := json.MarshalIndent(status, "", "  ")
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("Failed to format status: %v", err)), nil
	}

	return mcp.NewToolResultText(fmt.Sprintf("Web server started successfully!\n%s", string(output))), nil
}

func handleStopWebserver(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	if remoteSearcher != nil {
		return mcp.NewToolResultError("Not available in external mode (ZOEKT_URL is set). The webserver is managed externally."), nil
	}
	if err := webServerManager.Stop(); err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("Failed to stop web server: %v", err)), nil
	}

	return mcp.NewToolResultText("Web server stopped successfully"), nil
}

func handleWebserverStatus(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	if remoteSearcher != nil {
		return mcp.NewToolResultError("Not available in external mode (ZOEKT_URL is set). The webserver is managed externally."), nil
	}
	status := webServerManager.Status()

	output, err := json.MarshalIndent(status, "", "  ")
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("Failed to format status: %v", err)), nil
	}

	return mcp.NewToolResultText(string(output)), nil
}
