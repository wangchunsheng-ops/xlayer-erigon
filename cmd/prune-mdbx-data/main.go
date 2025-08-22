package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
)

func main() {
	if len(os.Args) < 2 {
		printUsage()
		os.Exit(1)
	}

	command := os.Args[1]

	switch command {
	case "list-tables":
		if len(os.Args) != 3 {
			fmt.Println("Usage: prune-mdbx-data list-tables <db_path>")
			os.Exit(1)
		}
		runListTables(os.Args[2])
	case "prune-chaindata":
		runPruneChaindata(os.Args[2:])
	case "help":
		printUsage()
	default:
		fmt.Printf("Unknown command: %s\n", command)
		printUsage()
		os.Exit(1)
	}
}

func runListTables(dbPath string) {
	// Build and run the list-tables subcommand
	cmdDir := filepath.Join("cmd", "list-tables")
	if err := os.Chdir(cmdDir); err != nil {
		fmt.Printf("Error: failed to change to %s directory: %v\n", cmdDir, err)
		os.Exit(1)
	}

	// Build the command
	buildCmd := exec.Command("go", "build", "-o", "list-tables-tool", "main.go")
	if err := buildCmd.Run(); err != nil {
		fmt.Printf("Error: failed to build list-tables: %v\n", err)
		os.Exit(1)
	}

	// Run the command
	runCmd := exec.Command("./list-tables-tool", "../../"+dbPath)
	runCmd.Stdout = os.Stdout
	runCmd.Stderr = os.Stderr
	if err := runCmd.Run(); err != nil {
		fmt.Printf("Error: failed to run list-tables: %v\n", err)
		os.Exit(1)
	}

	// Clean up
	os.Remove("list-tables-tool")
	os.Chdir("../..")
}

func runPruneChaindata(args []string) {
	if len(args) < 1 {
		fmt.Println("Usage: prune-mdbx-data prune-chaindata <db_path> [level] [options]")
		fmt.Println("Run 'prune-mdbx-data help' for more information")
		os.Exit(1)
	}

	// Build and run the prune-chaindata subcommand
	cmdDir := filepath.Join("cmd", "prune-chaindata")
	if err := os.Chdir(cmdDir); err != nil {
		fmt.Printf("Error: failed to change to %s directory: %v\n", cmdDir, err)
		os.Exit(1)
	}

	// Build the command
	buildCmd := exec.Command("go", "build", "-o", "prune-chaindata-tool", "main.go")
	if err := buildCmd.Run(); err != nil {
		fmt.Printf("Error: failed to build prune-chaindata: %v\n", err)
		os.Exit(1)
	}

	// Prepare arguments
	cmdArgs := make([]string, 0, len(args)+1)
	cmdArgs = append(cmdArgs, "../../"+args[0]) // db_path

	// Add remaining arguments
	if len(args) > 1 {
		cmdArgs = append(cmdArgs, args[1:]...)
	}

	// Run the command
	runCmd := exec.Command("./prune-chaindata-tool", cmdArgs...)
	runCmd.Stdout = os.Stdout
	runCmd.Stderr = os.Stderr
	if err := runCmd.Run(); err != nil {
		fmt.Printf("Error: failed to run prune-chaindata: %v\n", err)
		os.Exit(1)
	}

	// Clean up
	os.Remove("prune-chaindata-tool")
	os.Chdir("../..")
}

func printUsage() {
	fmt.Println("🗂️  X Layer MDBX Data Pruning Tool")
	fmt.Println()
	fmt.Println("A comprehensive database management tool for X Layer (zkEVM) Erigon nodes")
	fmt.Println()
	fmt.Println("USAGE:")
	fmt.Println("  prune-mdbx-data <command> [arguments]")
	fmt.Println()
	fmt.Println("COMMANDS:")
	fmt.Println("  list-tables <db_path>                   📋 List all database tables with sizes and statistics")
	fmt.Println("  prune-chaindata <db_path> [level] [options]  🧹 Prune unnecessary data from chaindata")
	fmt.Println("  help                                     ❓ Show detailed help information")
	fmt.Println()
	fmt.Println("PRUNING LEVELS (prune-chaindata):")
	fmt.Println("  conservative (default)                  🛡️  Conservative - safe minimal cleanup")
	fmt.Println("                                             Strategy: Only delete obviously unnecessary tables")
	fmt.Println("                                             Deletes: Only Beacon tables + diagnostic tables (~45 tables)")
	fmt.Println()
	fmt.Println("  moderate                                ⚖️  Moderate - comprehensive cleanup with batch optimization")
	fmt.Println("                                             Strategy: Delete verified unnecessary tables + batch-based pruning")
	fmt.Println("                                             Deletes: History, Index, Trie, Beacon, Diagnostic tables + old batch data")
	fmt.Println("                                             zkEVM optimized: Balance space saving with operational capability")
	fmt.Println()
	fmt.Println("PRUNING OPTIONS (moderate level):")
	fmt.Println("  --keep-recent-batches=N                 🎯 Keep N most recent batches (default: 10)")
	fmt.Println("  --yes, -y                               ⚡ Auto-confirm, skip interactive confirmation")
	fmt.Println()
	fmt.Println("EXAMPLES:")
	fmt.Println("  # List all database tables")
	fmt.Println("  prune-mdbx-data list-tables ./datadir")
	fmt.Println()
	fmt.Println("  # Conservative pruning (interactive)")
	fmt.Println("  prune-mdbx-data prune-chaindata ./datadir conservative")
	fmt.Println()
	fmt.Println("  # Moderate pruning with custom batch retention")
	fmt.Println("  prune-mdbx-data prune-chaindata ./datadir moderate --keep-recent-batches=5")
	fmt.Println()
	fmt.Println("  # Non-interactive moderate pruning")
	fmt.Println("  prune-mdbx-data prune-chaindata ./datadir moderate --keep-recent-batches=10 --yes")
	fmt.Println()
	fmt.Println("SAFETY NOTES:")
	fmt.Println("  ⚠️  Always backup your data before pruning")
	fmt.Println("  ⚠️  Moderate level uses batch-based deletion optimized for zkEVM")
	fmt.Println("  ⚠️  All zkEVM critical tables are automatically protected")
	fmt.Println("  ⚠️  Use --yes flag carefully in production environments")
	fmt.Println()
	fmt.Println("For more information, see: cmd/prune-mdbx-data/README.md")
}
