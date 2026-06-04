package main

import (
	"errors"
	"flag"
	"fmt"
	"strconv"

	"github.com/fatih/color"

	"github.com/7c/pingmon/classes/store"
)

func init() {
	registerCommand(command{
		name:    "group",
		summary: "Manage groups: group add|edit|list|assign|unassign (no server/token)",
		run:     runGroupCmd,
	})
}

func runGroupCmd(args []string) int {
	if len(args) == 0 {
		fmt.Fprintln(color.Error, "usage: pingmon group <add|edit|list|assign|unassign> [args]")
		return 2
	}
	switch args[0] {
	case "add":
		return groupAdd(args[1:])
	case "edit":
		return groupEdit(args[1:])
	case "list", "ls":
		return groupList(args[1:])
	case "assign":
		return groupAssign(args[1:], true)
	case "unassign":
		return groupAssign(args[1:], false)
	default:
		fmt.Fprintf(color.Error, "unknown group subcommand: %s\n", args[0])
		return 2
	}
}

func groupAdd(args []string) int {
	fs := flag.NewFlagSet("group add", flag.ExitOnError)
	df := addStoreFlags(fs)
	color_ := fs.String("color", "", "color tag")
	desc := fs.String("desc", "", "description")
	fs.Usage = func() {
		fmt.Fprintln(color.Output, "usage: pingmon group add [--color C] [--desc D] <name>")
	}
	_ = fs.Parse(args)
	if fs.NArg() != 1 {
		fmt.Fprintln(color.Error, "group add: exactly one <name> is required")
		return 2
	}

	st, _, err := openStoreFromFlags(*df)
	if err != nil {
		fmt.Fprintf(color.Error, "group add: %v\n", err)
		return 1
	}
	defer st.Close()

	g, err := st.CreateGroup(fs.Arg(0), *color_, *desc)
	if err != nil {
		if errors.Is(err, store.ErrConflict) {
			fmt.Fprintf(color.Error, "group add: a group named %q already exists\n", fs.Arg(0))
		} else {
			fmt.Fprintf(color.Error, "group add: %v\n", err)
		}
		return 1
	}
	fmt.Fprintf(color.Output, "created group %d (%s)\n", g.ID, g.Name)
	return 0
}

func groupEdit(args []string) int {
	fs := flag.NewFlagSet("group edit", flag.ExitOnError)
	df := addStoreFlags(fs)
	name := fs.String("name", "", "new name")
	color_ := fs.String("color", "", "new color")
	desc := fs.String("desc", "", "new description")
	fs.Usage = func() {
		fmt.Fprintln(color.Output, "usage: pingmon group edit [--name N] [--color C] [--desc D] <id>")
	}
	_ = fs.Parse(args)
	if fs.NArg() != 1 {
		fmt.Fprintln(color.Error, "group edit: exactly one <id> is required")
		return 2
	}
	id, err := strconv.ParseInt(fs.Arg(0), 10, 64)
	if err != nil {
		fmt.Fprintf(color.Error, "group edit: invalid id %q\n", fs.Arg(0))
		return 2
	}

	st, _, err := openStoreFromFlags(*df)
	if err != nil {
		fmt.Fprintf(color.Error, "group edit: %v\n", err)
		return 1
	}
	defer st.Close()

	// Load existing, then override only the flags that were provided (so unset
	// flags don't clobber existing values).
	g, err := st.GetGroup(id)
	if err != nil {
		fmt.Fprintf(color.Error, "group edit: %v\n", err)
		return 1
	}
	set := map[string]bool{}
	fs.Visit(func(f *flag.Flag) { set[f.Name] = true })
	if set["name"] {
		g.Name = *name
	}
	if set["color"] {
		g.Color = *color_
	}
	if set["desc"] {
		g.Description = *desc
	}
	if err := st.UpdateGroup(g.ID, g.Name, g.Color, g.Description); err != nil {
		fmt.Fprintf(color.Error, "group edit: %v\n", err)
		return 1
	}
	fmt.Fprintf(color.Output, "updated group %d (%s)\n", g.ID, g.Name)
	return 0
}

func groupList(args []string) int {
	fs := flag.NewFlagSet("group list", flag.ExitOnError)
	df := addStoreFlags(fs)
	_ = fs.Parse(args)

	st, _, err := openStoreFromFlags(*df)
	if err != nil {
		fmt.Fprintf(color.Error, "group list: %v\n", err)
		return 1
	}
	defer st.Close()

	groups, err := st.ListGroups()
	if err != nil {
		fmt.Fprintf(color.Error, "group list: %v\n", err)
		return 1
	}
	out := color.Output
	header := color.New(color.FgCyan, color.Bold).SprintfFunc()
	dim := color.New(color.Faint).SprintFunc()
	if len(groups) == 0 {
		fmt.Fprintln(out, dim("No groups."))
		return 0
	}
	fmt.Fprintf(out, "%s\n", header("%-5s %-20s %-10s %s", "ID", "NAME", "COLOR", "DESCRIPTION"))
	for _, g := range groups {
		ips, _ := st.ListHostsInGroup(g.ID)
		fmt.Fprintf(out, "%-5d %-20s %-10s %s\n", g.ID, g.Name, g.Color,
			dim(fmt.Sprintf("%s (%d hosts)", g.Description, len(ips))))
	}
	return 0
}

// groupAssign assigns (assign=true) or unassigns (assign=false) hosts to a group.
func groupAssign(args []string, assign bool) int {
	verb := "assign"
	if !assign {
		verb = "unassign"
	}
	fs := flag.NewFlagSet("group "+verb, flag.ExitOnError)
	df := addStoreFlags(fs)
	fs.Usage = func() {
		fmt.Fprintf(color.Output, "usage: pingmon group %s <group-id> <ip> [<ip>...]\n", verb)
	}
	_ = fs.Parse(args)
	if fs.NArg() < 2 {
		fmt.Fprintf(color.Error, "group %s: need <group-id> and at least one <ip>\n", verb)
		return 2
	}
	id, err := strconv.ParseInt(fs.Arg(0), 10, 64)
	if err != nil {
		fmt.Fprintf(color.Error, "group %s: invalid id %q\n", verb, fs.Arg(0))
		return 2
	}

	st, _, err := openStoreFromFlags(*df)
	if err != nil {
		fmt.Fprintf(color.Error, "group %s: %v\n", verb, err)
		return 1
	}
	defer st.Close()

	if _, err := st.GetGroup(id); err != nil {
		fmt.Fprintf(color.Error, "group %s: %v\n", verb, err)
		return 1
	}

	rc := 0
	for _, ip := range fs.Args()[1:] {
		var e error
		if assign {
			e = st.AssignHostToGroup(ip, id)
		} else {
			e = st.UnassignHostFromGroup(ip, id)
		}
		if e != nil {
			fmt.Fprintf(color.Error, "  %s: %v\n", ip, e)
			rc = 1
			continue
		}
		fmt.Fprintf(color.Output, "%sed %s %s group %d\n", verb, ip, map[bool]string{true: "to", false: "from"}[assign], id)
	}
	return rc
}
