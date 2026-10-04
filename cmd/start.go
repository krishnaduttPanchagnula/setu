package cmd

import (
	"bufio"
	stdcontext "context"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"setu/pkg/config"
	"setu/pkg/context"
	"setu/pkg/runner"
	"setu/pkg/storage"
	"setu/pkg/ticket"
)

// Version is stamped at build time via -ldflags.
var Version = "0.1.0"

// startOptions holds parsed flags for `setu start` / `setu list`.
type startOptions struct {
	configPath   string
	harness      string
	assignee     string
	status       string
	ticketID     string
	title        string
	requirement  string
	manual       bool
	dryRun       bool
	noCommit     bool
	noConfluence bool
}

func parseFlags(args []string) (startOptions, error) {
	var o startOptions
	for i := 0; i < len(args); i++ {
		arg := args[i]
		next := func(flag string) (string, error) {
			if i+1 >= len(args) {
				return "", fmt.Errorf("missing value for %s", flag)
			}
			i++
			return args[i], nil
		}
		var err error
		switch arg {
		case "--config":
			o.configPath, err = next(arg)
		case "--harness":
			o.harness, err = next(arg)
		case "--assignee":
			o.assignee, err = next(arg)
		case "--status":
			o.status, err = next(arg)
		case "--ticket":
			o.ticketID, err = next(arg)
		case "--title":
			o.title, err = next(arg)
		case "--requirement":
			o.requirement, err = next(arg)
		case "--manual":
			o.manual = true
		case "--dry-run":
			o.dryRun = true
		case "--no-commit":
			o.noCommit = true
		case "--no-confluence":
			o.noConfluence = true
		default:
			return o, fmt.Errorf("unknown flag %q", arg)
		}
		if err != nil {
			return o, err
		}
	}
	return o, nil
}

// resolveProvider picks the ticketing source per config/flags (PRD 1.2 step 2).
func resolveProvider(cfg *config.Config, o startOptions) (ticket.Provider, error) {
	if o.manual || cfg.Ticketing.Provider == "manual" {
		if o.ticketID != "" {
			return ticket.NewFromFlag(o.ticketID, o.title, o.requirement), nil
		}
		// Look for a local work-items file next to the config for manual mode.
		return ticket.NewManualProvider(findManualFile(o.configPath))
	}
	switch cfg.Ticketing.Provider {
	case "jira":
		if cfg.Ticketing.Jira.URL == "" {
			return nil, fmt.Errorf("ticketing.provider=jira but ticketing.jira.url is not set")
		}
		return ticket.NewJiraProvider(cfg.Ticketing.Jira.URL, cfg.Ticketing.Jira.Username, cfg.Ticketing.Jira.Token), nil
	case "azure":
		if cfg.Ticketing.Azure.URL == "" {
			return nil, fmt.Errorf("ticketing.provider=azure but ticketing.azure.url is not set")
		}
		return ticket.NewAzureProvider(
			cfg.Ticketing.Azure.URL,
			cfg.Ticketing.Azure.Org,
			cfg.Ticketing.Azure.Project,
			cfg.Ticketing.Azure.PAT,
		), nil
	default:
		return nil, fmt.Errorf("unsupported ticketing provider %q", cfg.Ticketing.Provider)
	}
}

// findManualFile returns work-items.json beside the config file if present.
func findManualFile(configPath string) string {
	if configPath == "" {
		configPath = config.DefaultPath()
	}
	candidate := strings.TrimSuffix(configPath, ".yaml") + "-work-items.json"
	if _, err := os.Stat(candidate); err == nil {
		return candidate
	}
	for _, name := range []string{"work-items.json", ".ai-context/work-items.json"} {
		if _, err := os.Stat(name); err == nil {
			return name
		}
	}
	return ""
}

// selectItem renders an interactive menu (PRD 1.2 step 3) on out, reads the
// choice from in, and returns the picked item.
func selectItem(items []ticket.WorkItem, in io.Reader, out io.Writer) (ticket.WorkItem, error) {
	if len(items) == 0 {
		return ticket.WorkItem{}, fmt.Errorf("no work items match the filter")
	}
	if len(items) == 1 {
		fmt.Fprintf(out, "Selected: %s\n", items[0])
		return items[0], nil
	}

	fmt.Fprintln(out, "Select a work item:")
	for i, item := range items {
		fmt.Fprintf(out, "  [%d] %s\n", i+1, item)
	}
	fmt.Fprint(out, "Choice (number or q): ")

	scanner := bufio.NewScanner(in)
	if !scanner.Scan() {
		return ticket.WorkItem{}, fmt.Errorf("no selection provided")
	}
	choice := strings.TrimSpace(scanner.Text())
	if choice == "q" || choice == "quit" {
		return ticket.WorkItem{}, fmt.Errorf("selection cancelled")
	}
	var idx int
	if _, err := fmt.Sscanf(choice, "%d", &idx); err != nil {
		return ticket.WorkItem{}, fmt.Errorf("invalid choice %q", choice)
	}
	if idx < 1 || idx > len(items) {
		return ticket.WorkItem{}, fmt.Errorf("choice %d out of range 1-%d", idx, len(items))
	}
	fmt.Fprintf(out, "Selected: %s\n", items[idx-1])
	return items[idx-1], nil
}

// runStart executes the core user flow from PRD 1.2.
func runStart(args []string, in io.Reader, out, errOut io.Writer) int {
	opts, err := parseFlags(args)
	if err != nil {
		fmt.Fprintf(errOut, "setu start: %v\n", err)
		return 2
	}

	cfg, err := config.Load(opts.configPath)
	if err != nil {
		fmt.Fprintf(errOut, "setu start: %v\n", err)
		return 1
	}

	harnessName := cfg.Core.DefaultHarness
	if opts.harness != "" {
		harnessName = opts.harness
	}
	status := cfg.Ticketing.StatusFilter
	if opts.status != "" {
		status = opts.status
	}
	assignee := opts.assignee
	if assignee == "" {
		assignee = cfg.Ticketing.Assignee
	}

	// Step 2: context resolution.
	provider, err := resolveProvider(cfg, opts)
	if err != nil {
		fmt.Fprintf(errOut, "setu start: %v\n", err)
		return 1
	}
	items, err := provider.GetActiveWorkItems(assignee, status)
	if err != nil {
		fmt.Fprintf(errOut, "setu start: fetch work items: %v\n", err)
		return 1
	}

	// Optional ID narrowing.
	if opts.ticketID != "" && !opts.manual {
		narrowed := items[:0:0]
		for _, item := range items {
			if item.ID == opts.ticketID {
				narrowed = append(narrowed, item)
			}
		}
		items = narrowed
	}
	if len(items) == 0 {
		fmt.Fprintf(errOut, "setu start: no work items for assignee=%q status=%q\n", assignee, status)
		return 1
	}

	// Step 3: selection.
	selected, err := selectItem(items, in, out)
	if err != nil {
		fmt.Fprintf(errOut, "setu start: %v\n", err)
		return 1
	}
	if err := selected.Validate(); err != nil {
		fmt.Fprintf(errOut, "setu start: %v\n", err)
		return 1
	}

	// Step 4: workspace preparation.
	builder := context.NewBuilder(cfg.Core.WorkspaceDir)
	h, err := runner.NewFromSpec(harnessName, cfg.HarnessSpec(harnessName), builder)
	if err != nil {
		fmt.Fprintf(errOut, "setu start: %v\n", err)
		return 1
	}
	if err := h.PrepareContext(selected, cfg.Core.WorkspaceDir); err != nil {
		fmt.Fprintf(errOut, "setu start: prepare context: %v\n", err)
		return 1
	}
	// Wire the CLI's streams so the subprocess inherits the caller's terminal
	// (tests can capture it too).
	h.Stdin = in
	h.Stdout = out
	h.Stderr = errOut
	fmt.Fprintf(out, "Prepared %s context for %s\n", harnessName, selected.ID)

	startedAt := time.Now()

	// Step 5: delegation. A harness failure is reported after persistence so
	// the session artifacts are still saved, but the process exit code
	// reflects the failure instead of masking it.
	var harnessErr error
	if opts.dryRun {
		fmt.Fprintln(out, "Dry run: skipping harness execution")
	} else {
		fmt.Fprintf(out, "Starting harness %q (exit the tool to return)...\n", harnessName)
		if err := h.Execute(stdcontext.Background()); err != nil {
			harnessErr = err
			fmt.Fprintf(errOut, "setu start: harness error: %v\n", err)
			// Fall through: still persist whatever context exists.
		}
	}

	// Step 6: persistence (PRD 1.2 step 6).
	finishedAt := time.Now()
	if err := builder.AppendSessionNote(selected, fmt.Sprintf(
		"session end: harness=%s duration=%s", harnessName, finishedAt.Sub(startedAt).Round(time.Millisecond),
	)); err != nil {
		fmt.Fprintf(errOut, "setu start: session log: %v\n", err)
	}

	if !opts.noCommit {
		committer := storage.NewGitCommitter(cfg.Core.Author.Name, cfg.Core.Author.Email)
		hash, err := committer.Commit(cfg.Core.WorkspaceDir, fmt.Sprintf("setu: persist %s (%s)", selected.ID, selected.Title))
		if err != nil {
			fmt.Fprintf(errOut, "setu start: git commit: %v\n", err)
		} else {
			fmt.Fprintf(out, "Committed workspace %s at %.8s\n", cfg.Core.WorkspaceDir, hash)
		}
	}

	if cfg.Confluence.Enabled && !opts.noConfluence && cfg.Confluence.Token != "" {
		summary := fmt.Sprintf("# %s\n\n## %s\n\n%s\n", selected.ID, selected.Title, selected.Requirement)
		client := storage.NewConfluenceClient(
			firstNonEmpty(cfg.Confluence.BaseURL, cfg.Ticketing.Jira.URL),
			cfg.Confluence.Token,
			cfg.Confluence.SpaceKey,
			cfg.Confluence.ParentPageID,
		)
		pageID, err := client.PushSummary(stdcontext.Background(), storage.SessionSummary{
			Item:       selected,
			Markdown:   summary,
			StartedAt:  startedAt,
			FinishedAt: finishedAt,
		})
		if err != nil {
			fmt.Fprintf(errOut, "setu start: confluence push: %v\n", err)
		} else {
			fmt.Fprintf(out, "Pushed Confluence page %s\n", pageID)
		}
	}

	if harnessErr != nil {
		fmt.Fprintln(out, "Session ended with harness failure (context persisted).")
		return 1
	}
	fmt.Fprintln(out, "Session complete.")
	return 0
}

// runList prints matching work items without delegating (debugging aid).
func runList(args []string, out, errOut io.Writer) int {
	opts, err := parseFlags(args)
	if err != nil {
		fmt.Fprintf(errOut, "setu list: %v\n", err)
		return 2
	}
	cfg, err := config.Load(opts.configPath)
	if err != nil {
		fmt.Fprintf(errOut, "setu list: %v\n", err)
		return 1
	}
	provider, err := resolveProvider(cfg, opts)
	if err != nil {
		fmt.Fprintf(errOut, "setu list: %v\n", err)
		return 1
	}
	status := cfg.Ticketing.StatusFilter
	if opts.status != "" {
		status = opts.status
	}
	assignee := opts.assignee
	if assignee == "" {
		assignee = cfg.Ticketing.Assignee
	}
	items, err := provider.GetActiveWorkItems(assignee, status)
	if err != nil {
		fmt.Fprintf(errOut, "setu list: %v\n", err)
		return 1
	}
	if len(items) == 0 {
		fmt.Fprintln(out, "(no work items)")
		return 0
	}
	for _, item := range items {
		fmt.Fprintf(out, "%s\t%s\t%s\n", item.ID, item.Title, item.URL)
	}
	return 0
}

func runConfigInit(args []string, out, errOut io.Writer) int {
	path := config.DefaultPath()
	if len(args) == 2 && args[0] == "--config" {
		path = args[1]
	}
	if err := config.WriteExample(path); err != nil {
		fmt.Fprintf(errOut, "setu config init: %v\n", err)
		return 1
	}
	fmt.Fprintf(out, "Wrote example config to %s\n", path)
	return 0
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}
