package cmd

import (
	"fmt"
	"strconv"
	"strings"
	"text/tabwriter"

	"github.com/Raina-Hardik/smelt/internal/config"
	"github.com/spf13/cobra"
)

var profilesCmd = &cobra.Command{
	Use:   "profiles",
	Short: "List available transcode profiles",
	Long: `List the transcode profiles available to --profile.

A profile is a composable, preconfigured set of transcode flags. Built-in
profiles ship in the binary and work with no config file; you can define your
own (or override a built-in of the same name, field by field) under the
'profiles' section of config.yaml.

With no arguments, lists every profile. 'smelt profiles show <name>' prints the
exact 'smelt transcode' flags that --profile <name> expands to.`,
	Example: `  smelt profiles
  smelt profiles show web
  smelt transcode --src /mnt/media --profile archive`,
	Args: cobra.NoArgs,
	RunE: runProfilesList,
}

var profilesShowCmd = &cobra.Command{
	Use:   "show <name>",
	Short: "Show the flags a profile expands to",
	Long: `Print the details of one profile: its source (built-in or config), its
description, and the exact 'smelt transcode' flags --profile <name> injects.
Explicit flags on the command line still override these.`,
	Example: `  smelt profiles show web
  smelt profiles show archive`,
	Args: cobra.ExactArgs(1),
	ValidArgsFunction: func(_ *cobra.Command, args []string, _ string) ([]string, cobra.ShellCompDirective) {
		if len(args) != 0 {
			return nil, cobra.ShellCompDirectiveNoFileComp
		}
		return config.ProfileNames(), cobra.ShellCompDirectiveNoFileComp
	},
	RunE: runProfilesShow,
}

func init() {
	profilesCmd.AddCommand(profilesShowCmd)
	rootCmd.AddCommand(profilesCmd)
}

func runProfilesList(cmd *cobra.Command, _ []string) error {
	names := config.ProfileNames()
	if len(names) == 0 {
		_, _ = fmt.Fprintln(cmd.OutOrStdout(), "no profiles available")
		return nil
	}

	builtin := map[string]bool{}
	for _, n := range config.BuiltinProfileNames() {
		builtin[n] = true
	}

	w := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 0, 2, ' ', 0)
	_, _ = fmt.Fprintln(w, "NAME\tSOURCE\tCODEC\tCRF\tDESCRIPTION")
	for _, name := range names {
		p, ok := config.ResolveProfile(name)
		if !ok {
			continue
		}
		source := "config"
		if builtin[name] {
			source = "built-in"
			if config.HasConfigProfile(name) {
				source = "overridden"
			}
		}
		_, _ = fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\n",
			name, source, dash(p.Codec), crfStr(p.CRF), dash(p.Description))
	}
	_ = w.Flush()
	_, _ = fmt.Fprintln(cmd.OutOrStdout(), "\nRun 'smelt profiles show <name>' to see the flags a profile expands to.")
	return nil
}

func runProfilesShow(cmd *cobra.Command, args []string) error {
	name := args[0]
	p, ok := config.ResolveProfile(name)
	if !ok {
		return exitErr(2, fmt.Errorf("unknown profile %q; run `smelt profiles` to list them", name))
	}

	source := "config.yaml"
	if _, isBuiltin := config.BuiltinProfile(name); isBuiltin {
		source = "built-in"
		if config.HasConfigProfile(name) {
			source = "built-in (overridden by config.yaml)"
		}
	}

	out := cmd.OutOrStdout()
	_, _ = fmt.Fprintf(out, "profile: %s\n", p.Name)
	_, _ = fmt.Fprintf(out, "source:  %s\n", source)
	if p.Description != "" {
		_, _ = fmt.Fprintf(out, "summary: %s\n", p.Description)
	}
	_, _ = fmt.Fprintln(out, "\nexpands to:")
	_, _ = fmt.Fprintf(out, "  smelt transcode --profile %s\n", name)
	_, _ = fmt.Fprintln(out, "\nequivalent explicit flags:")
	flags := profileFlags(p)
	if len(flags) == 0 {
		_, _ = fmt.Fprintln(out, "  (profile sets no flags)")
		return nil
	}
	_, _ = fmt.Fprintf(out, "  %s\n", strings.Join(flags, " "))
	return nil
}

// profileFlags renders the flags a profile injects, in the same order and form
// as the CLI accepts them. It intentionally shows only the flags the profile
// sets — not a full command line — so the reader sees exactly what --profile
// contributes on top of the defaults and any explicit flags.
func profileFlags(p config.Profile) []string {
	var f []string
	if p.Codec != "" {
		f = append(f, "--codec", p.Codec)
	}
	if p.CRF != nil {
		f = append(f, "--crf", strconv.Itoa(*p.CRF))
	}
	if p.Preset != "" {
		f = append(f, "--preset", p.Preset)
	}
	if p.AudioCodec != "" {
		f = append(f, "--audio-codec", p.AudioCodec)
	}
	if p.AudioBitrate != "" {
		f = append(f, "--audio-bitrate", p.AudioBitrate)
	}
	if p.Container != "" {
		f = append(f, "--to", p.Container)
	}
	if p.Subs != "" {
		f = append(f, "--subs", p.Subs)
	}
	for _, a := range p.ExtraArgs {
		f = append(f, "--ffmpeg-arg", a)
	}
	return f
}

func dash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

func crfStr(n *int) string {
	if n == nil {
		return "-"
	}
	return strconv.Itoa(*n)
}
