package main

import (
	"flag"
	"fmt"
	"log"
	"os"
)

func main() {
	var (
		author     string
		output     string
		repoPath   string
		category   string
		since      string
		until      string
		dateFormat string
		estimate   float64
	)

	flag.StringVar(&author, "author", "", "Author name pattern used to filter git commits (required).")
	flag.StringVar(&output, "output", "git_commits.xlsx", "Output xlsx file path.")
	flag.StringVar(&repoPath, "repo", ".", "Path to the git repository.")
	flag.StringVar(&category, "category", "MF", "Value for the 需求类别 column.")
	flag.Float64Var(&estimate, "estimate", 0.5, "Value for the 预估工时 column.")
	flag.StringVar(&since, "since", "", "Optional --since value passed to git log.")
	flag.StringVar(&until, "until", "", "Optional --until value passed to git log.")
	flag.StringVar(&dateFormat, "date-format", "2006-01-02", "Date format for the 预计开始/预计结束 columns, default YYYY-MM-DD.")
	flag.Parse()

	if author == "" {
		fmt.Fprintln(os.Stderr, "author is required.")
		flag.Usage()
		os.Exit(2)
	}

	commits, err := collectGitCommits(repoPath, author, since, until)
	if err != nil {
		log.Fatalf("failed to read git log: %v", err)
	}

	opts := exportOptions{
		OutputPath: output,
		Category:   category,
		Estimate:   estimate,
		DateFormat: dateFormat,
		SheetName:  defaultSheetName,
	}

	if err := exportXLSX(commits, opts); err != nil {
		log.Fatalf("failed to export xlsx: %v", err)
	}

	fmt.Printf("Exported %d commits to %s\n", len(commits), output)
}
