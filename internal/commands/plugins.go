package commands

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"path/filepath"
	"strings"
	"text/tabwriter"

	"github.com/infracost/ci/internal/config"
	"github.com/infracost/cli/pkg/plugins"
	pkgscanner "github.com/infracost/cli/pkg/scanner"
	"github.com/spf13/cobra"
)

const (
	formatTable = "table"
	formatJSON  = "json"
)

// pluginItem is the --format json shape. plugins.ListItem is not marshalled
// directly: its field names are the output contract the image verify asserts on.
type pluginItem struct {
	Key       string `json:"key"`
	Name      string `json:"name"`
	Type      string `json:"type"`
	Path      string `json:"path"`
	Installed bool   `json:"installed"`
	Required  bool   `json:"required"`
	Version   string `json:"version"`
}

type pluginsDetectArgs struct {
	path string
}

func Plugins(cfg *config.Config) *cobra.Command {
	pluginsCmd := &cobra.Command{
		Use:   "plugins",
		Short: "Manage the parser and provider plugins the scanner loads",
	}

	pluginsCmd.AddCommand(pluginsInstallCommand(cfg))
	pluginsCmd.AddCommand(pluginsListCommand(cfg))
	pluginsCmd.AddCommand(pluginsDetectCommand(cfg))

	return pluginsCmd
}

// pluginsInstallCommand installs the required plugins without running a scan,
// so a container build can bake them in. It needs no authentication token.
func pluginsInstallCommand(cfg *config.Config) *cobra.Command {
	return &cobra.Command{
		Use:   "install",
		Short: "Install the required parser and provider plugins",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			// UpdatePlugins, not EnsurePlugins: it installs every required
			// plugin whatever INFRACOST_CLI_PLUGIN_AUTO_UPDATE is set to.
			if err := cfg.Plugins.UpdatePlugins(context.Background()); err != nil {
				return fmt.Errorf("failed to install plugins: %w", err)
			}
			// The downloads print nothing without a TTY, so a docker build log
			// would otherwise show no trace of the largest layer in the image.
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Installed plugins into %s\n", cfg.Plugins.PluginDir())
			return nil
		},
	}
}

func pluginsListCommand(cfg *config.Config) *cobra.Command {
	var format string

	listCmd := &cobra.Command{
		Use:   "list",
		Short: "List the parser and provider plugins and the versions they report",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			items := cfg.Plugins.List()
			if err := printPlugins(cmd.OutOrStdout(), format, items); err != nil {
				return err
			}
			return checkRequiredPlugins(items)
		},
	}

	listCmd.Flags().StringVar(&format, "format", formatTable, "Output format (table or json)")

	return listCmd
}

func pluginsDetectCommand(cfg *config.Config) *cobra.Command {
	var args pluginsDetectArgs

	detectCmd := &cobra.Command{
		Use:   "detect",
		Short: "Print the projects the parser plugins identify in a directory",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return detectProjects(cfg, cmd.OutOrStdout(), args.path)
		},
	}

	detectCmd.Flags().StringVar(&args.path, "path", "", "Path to the directory to inspect")

	_ = detectCmd.MarkFlagRequired("path")

	return detectCmd
}

func printPlugins(w io.Writer, format string, items []plugins.ListItem) error {
	switch format {
	case formatJSON:
		out := make([]pluginItem, 0, len(items))
		for _, item := range items {
			out = append(out, pluginItem{
				Key:       item.Key,
				Name:      item.Name,
				Type:      item.Type,
				Path:      item.Path,
				Installed: item.Installed,
				Required:  item.Required,
				Version:   item.Version,
			})
		}
		enc := json.NewEncoder(w)
		enc.SetIndent("", "  ")
		return enc.Encode(out)
	case formatTable:
		tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
		_, _ = fmt.Fprintln(tw, "NAME\tTYPE\tVERSION\tPATH")
		for _, item := range items {
			version := item.Version
			if !item.Installed {
				version = "not installed"
			}
			_, _ = fmt.Fprintf(tw, "%s\t%s\t%s\t%s\n", item.Name, item.Type, version, item.Path)
		}
		return tw.Flush()
	default:
		return fmt.Errorf("invalid format %q: must be %s or %s", format, formatTable, formatJSON)
	}
}

// checkRequiredPlugins supplies the error contract List() does not have: it
// returns no error, and reports a plugin that will not start over gRPC as
// version "unknown" rather than as a failure.
func checkRequiredPlugins(items []plugins.ListItem) error {
	var broken []string
	for _, item := range items {
		if !item.Required {
			continue
		}
		// Two required plugins share a key, so the binary name is the only
		// unambiguous label.
		name := filepath.Base(item.Path)
		switch {
		case !item.Installed:
			broken = append(broken, name+" (not installed)")
		case item.Version == "" || item.Version == "unknown":
			broken = append(broken, name+" (did not report a version)")
		}
	}

	if len(broken) > 0 {
		return fmt.Errorf("required plugins are unusable: %s", strings.Join(broken, ", "))
	}
	return nil
}

// detectProjects resolves the projects in a directory the way a scan would,
// but without a token or an API client, so it runs anywhere the plugins do.
func detectProjects(cfg *config.Config, w io.Writer, path string) error {
	ctx := context.Background()

	dir, err := filepath.Abs(path)
	if err != nil {
		return fmt.Errorf("failed to resolve absolute path for %q: %w", path, err)
	}

	// Autodetection delegates to the plugin identifiers, so a cold plugin
	// cache silently yields zero projects rather than an error.
	if _, err := cfg.Plugins.EnsurePlugins(ctx); err != nil {
		return fmt.Errorf("failed to install plugins: %w", err)
	}

	repoConfig, err := pkgscanner.LoadOrGenerateRepositoryConfig(ctx, dir, cfg.Plugins.PluginDir())
	if err != nil {
		return fmt.Errorf("repository configuration error: %w", err)
	}

	if len(repoConfig.Projects) == 0 {
		return fmt.Errorf("no projects found in %s", dir)
	}

	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	_, _ = fmt.Fprintln(tw, "NAME\tTYPE\tPATH")
	for _, project := range repoConfig.Projects {
		_, _ = fmt.Fprintf(tw, "%s\t%s\t%s\n", project.Name, project.Type, project.Path)
	}
	return tw.Flush()
}
