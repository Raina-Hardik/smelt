package cmd

import "github.com/spf13/cobra"

// Command groups keep `smelt --help` legible as the verb count grows: the
// headline actions must not sit alphabetized among plumbing and composition
// primitives. Group IDs are stable strings referenced by each command's
// GroupID; cobra panics at execution if a GroupID has no registered group,
// so the registration below is the single source of truth.
const (
	groupCore    = "core"
	groupInspect = "inspect"
	groupCompose = "compose"
	groupManage  = "manage"
)

func init() {
	rootCmd.AddGroup(
		&cobra.Group{ID: groupCore, Title: "Core Commands:"},
		&cobra.Group{ID: groupInspect, Title: "Inspect Commands:"},
		&cobra.Group{ID: groupCompose, Title: "Compose Commands:"},
		&cobra.Group{ID: groupManage, Title: "Manage Commands:"},
	)

	// The commands themselves are registered in their own files' init();
	// assigning GroupID here only mutates already-constructed package vars,
	// so file init order is irrelevant.
	groupAssignments := map[*cobra.Command]string{
		transcodeCmd: groupCore,
		tuiCmd:       groupCore,
		watchCmd:     groupCore,
		workflowCmd:  groupCore,

		checkCmd:    groupInspect,
		historyCmd:  groupInspect,
		profilesCmd: groupInspect,
		matchCmd:    groupInspect,

		doCmd:   groupCompose,
		eachCmd: groupCompose,

		configCmd:    groupManage,
		cleanCmd:     groupManage,
		finishRunCmd: groupManage,
		serveCmd:     groupManage,
	}
	for cmd, group := range groupAssignments {
		cmd.GroupID = group
	}
}
