package main

import (
	"fmt"
	"github.com/alexflint/go-arg"
	"os"
	"runtime"
)

// Define command structs
type ManifestInfoCmd struct {
	URL        string `arg:"positional,required" help:"Sophon Build URL"`
	OutputPath string `arg:"positional,required" help:"Path to output JSON file or - for stdout"`
}

type DownloadCmd struct {
	URL            string `arg:"positional,required" help:"Sophon Build URL"`
	FieldName      string `arg:"positional,required" help:"Matching field name (usually 'game')"`
	Path           string `arg:"positional,required" help:"Download output path"`
	ThreadCount    int    `arg:"positional" help:"Amount of threads to be used"`
	MaxConnections int    `arg:"positional" help:"Amount of max connections for HTTP client"`
}

// Root command struct
type Args struct {
	ManifestInfo *ManifestInfoCmd `arg:"subcommand:manifestinfo" help:"Fetch and output manifest information"`
	Download     *DownloadCmd     `arg:"subcommand:download" help:"Download assets"`
}

func main() {
	var args Args
	arg.MustParse(&args)

	switch {
	case args.ManifestInfo != nil:
		runManifestInfo(args.ManifestInfo.URL, args.ManifestInfo.OutputPath)

	case args.Download != nil:
		cmd := args.Download
		// Set default values if not provided
		if cmd.ThreadCount <= 0 {
			cmd.ThreadCount = runtime.NumCPU() // Use number of CPU cores
		}
		if cmd.MaxConnections <= 0 {
			cmd.MaxConnections = 128 // Default max connections
		}

		DownloadCommand(cmd.URL, cmd.FieldName, cmd.Path, cmd.ThreadCount, cmd.MaxConnections)

	default:
		fmt.Println("No command specified")
		os.Exit(1)
	}
}
