package main

import (
	"fmt"
	"os"
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
			fmt.Println("Usage: prune-smt-data list-tables <db_path>")
			os.Exit(1)
		}
		runListTables(os.Args[2])
	case "prune-chaindata":
		if len(os.Args) < 3 || len(os.Args) > 4 {
			fmt.Println("Usage: prune-smt-data prune-chaindata <db_path> [level]")
			fmt.Println("Levels: conservative (default), moderate, aggressive")
			os.Exit(1)
		}
		level := "conservative"
		if len(os.Args) == 4 {
			level = os.Args[3]
		}
		runPruneChaindata(os.Args[2], level)
	case "help":
		printUsage()
	default:
		fmt.Printf("Unknown command: %s\n", command)
		printUsage()
		os.Exit(1)
	}
}

func runListTables(dbPath string) {
	// This would call the list-tables subcommand logic
	// Using simple implementation for now
	fmt.Printf("List database tables: %s\n", dbPath)
	fmt.Println("Feature under development...")
}

func runPruneChaindata(dbPath string, level string) {
	// This would call the prune-chaindata subcommand logic
	// Using simple implementation for now
	fmt.Printf("Prune chaindata: %s (level: %s)\n", dbPath, level)
	fmt.Println("Feature under development...")
}

func printUsage() {
	fmt.Println("SMT Data Pruning Tool")
	fmt.Println()
	fmt.Println("Usage:")
	fmt.Println("  prune-smt-data <command> [arguments]")
	fmt.Println()
	fmt.Println("Commands:")
	fmt.Println("  list-tables <db_path>                   List all tables in database")
	fmt.Println("  prune-chaindata <db_path> [level]       Prune unnecessary data from chaindata")
	fmt.Println("  help                                     Show help information")
	fmt.Println()
	fmt.Println("Pruning levels (prune-chaindata):")
	fmt.Println("  conservative (default)                   Conservative pruning - only delete obviously unnecessary tables")
	fmt.Println("  moderate                                 Moderate pruning - delete more state data and system tables")
	fmt.Println("  aggressive                               Aggressive pruning - delete almost all tables including block and transaction data")
	fmt.Println()
	fmt.Println("Examples:")
	fmt.Println("  prune-smt-data list-tables /path/to/erigon/data")
	fmt.Println("  prune-smt-data prune-chaindata /path/to/erigon/data")
	fmt.Println("  prune-smt-data prune-chaindata /path/to/erigon/data conservative")
	fmt.Println("  prune-smt-data prune-chaindata /path/to/erigon/data aggressive")
}
