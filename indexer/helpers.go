package indexer

import (
	"fmt"
	"strings"

	"github.com/sourcegraph/zoekt"
)

// pathResolver returns the display path for a file match given its repository and file name.
type pathResolver func(repo, fileName string) string

// formatSearchResults converts raw zoekt search results into compact grep-like output.
// resolvePath is called for each file match to produce the display path.
func formatSearchResults(result *zoekt.SearchResult, opts SearchOptions, resolvePath pathResolver) *SearchResult {
	if opts.MaxFiles <= 0 {
		opts.MaxFiles = 20
	}
	if opts.MaxLinesPerFile <= 0 {
		opts.MaxLinesPerFile = 3
	}
	if opts.MaxLineLength <= 0 {
		opts.MaxLineLength = 200
	}

	sr := &SearchResult{
		TotalFiles:   len(result.Files),
		TotalMatches: 0,
	}

	filesProcessed := 0
	for _, fileMatch := range result.Files {
		if filesProcessed >= opts.MaxFiles {
			break
		}
		filesProcessed++

		fullPath := resolvePath(fileMatch.Repository, fileMatch.FileName)

		if opts.FilesOnly {
			sr.Lines = append(sr.Lines, fullPath)
			continue
		}

		// Collect matches from LineMatches
		linesAdded := 0
		for _, lineMatch := range fileMatch.LineMatches {
			sr.TotalMatches++
			if linesAdded >= opts.MaxLinesPerFile {
				continue
			}
			linesAdded++

			content := strings.TrimRight(string(lineMatch.Line), "\n\r")
			content = truncateLine(content, opts.MaxLineLength)

			sr.Lines = append(sr.Lines, fmt.Sprintf("%s:%d: %s",
				fullPath, lineMatch.LineNumber, content))
		}

		// Handle ChunkMatches if LineMatches is empty
		if len(fileMatch.LineMatches) == 0 {
			for _, chunk := range fileMatch.ChunkMatches {
				lines := strings.Split(string(chunk.Content), "\n")
				for i, line := range lines {
					if strings.TrimSpace(line) == "" {
						continue
					}
					sr.TotalMatches++
					if linesAdded >= opts.MaxLinesPerFile {
						continue
					}
					linesAdded++

					content := truncateLine(strings.TrimRight(line, "\r"), opts.MaxLineLength)
					lineNum := int(chunk.ContentStart.LineNumber) + i

					sr.Lines = append(sr.Lines, fmt.Sprintf("%s:%d: %s",
						fullPath, lineNum, content))
				}
			}
		}

		// Add indicator if there are more matches in this file
		totalInFile := len(fileMatch.LineMatches)
		if totalInFile == 0 {
			for _, chunk := range fileMatch.ChunkMatches {
				totalInFile += strings.Count(string(chunk.Content), "\n") + 1
			}
		}
		if totalInFile > opts.MaxLinesPerFile {
			sr.Lines = append(sr.Lines, fmt.Sprintf("  ... and %d more matches in this file",
				totalInFile-opts.MaxLinesPerFile))
		}
	}

	// Add summary if results were truncated
	if sr.TotalFiles > opts.MaxFiles {
		sr.Lines = append(sr.Lines, fmt.Sprintf("\n[Showing %d of %d files. Use max_files to see more]",
			opts.MaxFiles, sr.TotalFiles))
	}

	return sr
}

// truncateLine shortens a line to maxLen, adding ellipsis if truncated
func truncateLine(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	if maxLen <= 3 {
		return s[:maxLen]
	}
	return s[:maxLen-3] + "..."
}
