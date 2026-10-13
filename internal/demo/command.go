package demo

import (
	"bufio"
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/pocketbase/pocketbase/core"
	"github.com/spf13/cobra"

	"pocketadmin/internal/organization"
	"pocketadmin/internal/tree"
)

// Command returns the `demo` command tree.
//
// It is a subcommand of the binary and never a step of installation. A fresh install is
// empty and stays empty until somebody types one of these; a panel that quietly filled
// itself with example customers would be worse than one that showed nothing, because
// nobody would know which records were theirs.
func Command(app core.App) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "demo",
		Short: "Load or clear the example dataset",
		Long: "Load a coherent set of example data for a demonstration or a test.\n\n" +
			"This is never run by installation. A fresh install is empty.\n\n" +
			"Business data is cleared and rewritten. Configuration and identity -- accounts,\n" +
			"roles, permissions, menus, settings, integrations and scheduled tasks -- are never\n" +
			"touched: reloading the examples must not cost an operator their setup.",
	}

	var (
		reset  bool
		yes    bool
		dryRun bool
	)

	seed := &cobra.Command{
		Use:   "seed",
		Short: "Write the example dataset",
		RunE: func(cmd *cobra.Command, _ []string) error {
			if reset {
				if err := confirm(app, "Clear the example data and write it again", yes); err != nil {
					return err
				}
				if dryRun {
					return nil
				}
			}

			result, err := Seed(app, reset)
			report(result, err)
			return err
		},
	}
	seed.Flags().BoolVar(&reset, "reset", false,
		"clear the example data first, so the result is the same however many times it is run")
	seed.Flags().BoolVar(&yes, "yes", false, "do not ask before clearing")
	seed.Flags().BoolVar(&dryRun, "dry-run", false, "show what would be cleared and stop")

	clear := &cobra.Command{
		Use:   "clear",
		Short: "Remove the example data",
		RunE: func(cmd *cobra.Command, _ []string) error {
			if err := confirm(app, "Remove the example data", yes); err != nil {
				return err
			}
			if dryRun {
				return nil
			}

			result := NewResult()
			err := Clear(app, result)
			report(result, err)
			return err
		},
	}
	clear.Flags().BoolVar(&yes, "yes", false, "do not ask")
	clear.Flags().BoolVar(&dryRun, "dry-run", false, "show what would be removed and stop")

	status := &cobra.Command{
		Use:   "status",
		Short: "Show what the example dataset currently holds",
		RunE: func(cmd *cobra.Command, _ []string) error {
			counts := CountOwned(app)

			fmt.Println("Example data currently stored:")
			printCounts(counts, "  ")
			if len(counts) == 0 {
				fmt.Println("  (nothing)")
			}

			users := 0
			if n, ok := counts["users"]; ok {
				users = n
			}
			fmt.Printf("\nExample accounts: %d (addresses beginning %q)\n", users, DemoUserPrefix)

			fmt.Println("\nNever touched by this tool:")
			for _, name := range Unchanged() {
				fmt.Printf("  %s\n", name)
			}
			return nil
		},
	}

	// Every subcommand bootstraps the application and runs the migrations first.
	//
	// PocketBase's own commands do this internally; a command of our own does not, and
	// without it the tool runs against a database with no tables in it. The bootstrap is
	// what makes `demo seed` work on a directory that has never been served.
	all := []*cobra.Command{seed, clear, status}
	for _, sub := range all {
		sub.PreRunE = func(_ *cobra.Command, _ []string) error {
			return bringUp(app)
		}
	}

	cmd.AddCommand(seed, clear, status)
	return cmd
}

// bringUp bootstraps the application and applies the platform's migrations.
//
// It is idempotent: PocketBase records which migrations have run, so a second invocation
// does nothing rather than failing. Registration of the model hooks is deliberately not
// repeated here -- the demo writes through the module functions directly, and the hooks
// are for the serving process.
func bringUp(app core.App) error {
	if app.IsBootstrapped() {
		return app.RunAllMigrations()
	}

	if err := app.Bootstrap(); err != nil {
		return err
	}
	if err := app.RunAllMigrations(); err != nil {
		return err
	}

	// The tree and organisation hooks maintain derived columns -- a folder's path, a
	// department's ancestry. The demo writes into those collections, so without them the
	// rows would have empty derived fields and the trees would render flat.
	organization.Register(app)
	tree.Register(app, tree.Options{Collection: "file_folders", Label: "folder"})
	tree.Register(app, tree.Options{Collection: "product_categories", Label: "category"})

	return nil
}

// confirm prints what is about to be lost and asks.
//
// It is deliberately not a `--force` flag alone. The size of what is being removed is the
// thing an operator needs to see, and a prompt is the only place they will read it.
func confirm(app core.App, action string, assumeYes bool) error {
	counts := CountOwned(app)

	fmt.Printf("%s.\n\n", action)
	fmt.Println("This will clear:")
	if len(counts) == 0 {
		fmt.Println("  (nothing — the example data is already absent)")
	} else {
		printCounts(counts, "  ")
	}

	fmt.Println("\nThis will NOT touch:")
	for _, name := range Unchanged() {
		fmt.Printf("  %s\n", name)
	}

	if assumeYes {
		fmt.Println("\nContinuing because --yes was given.")
		return nil
	}

	if !isInteractive() {
		return fmt.Errorf("refusing to clear without --yes: standard input is not a terminal")
	}

	fmt.Print("\nType \"yes\" to continue: ")
	reader := bufio.NewReader(os.Stdin)
	answer, _ := reader.ReadString('\n')

	if strings.TrimSpace(strings.ToLower(answer)) != "yes" {
		return fmt.Errorf("cancelled")
	}
	return nil
}

// Unchanged returns the collections a reset never clears, for the prompt.
func Unchanged() []string { return Untouched() }

// report prints what a run did.
func report(result *Result, err error) {
	if err != nil {
		fmt.Fprintf(os.Stderr, "\nFailed: %v\n", err)
	}

	if result == nil {
		return
	}

	if len(result.Cleared) > 0 {
		fmt.Println("\nCleared:")
		printCounts(result.Cleared, "  ")
	}
	if len(result.Created) > 0 {
		fmt.Println("\nWritten:")
		printCounts(result.Created, "  ")
		fmt.Printf("\n  %d row(s) in total.\n", result.TotalCreated())
	}
	if len(result.Skipped) > 0 {
		sort.Strings(result.Skipped)
		fmt.Printf("\nSkipped %d collection(s) this database does not have: %s\n",
			len(result.Skipped), strings.Join(result.Skipped, ", "))
	}
}

// printCounts prints a collection-to-count map in a stable order.
func printCounts(counts map[string]int, indent string) {
	names := make([]string, 0, len(counts))
	for name := range counts {
		names = append(names, name)
	}
	sort.Strings(names)

	for _, name := range names {
		fmt.Printf("%s%-28s %d\n", indent, name, counts[name])
	}
}

// isInteractive reports whether standard input is a terminal.
func isInteractive() bool {
	info, err := os.Stdin.Stat()
	if err != nil {
		return false
	}
	return info.Mode()&os.ModeCharDevice != 0
}
