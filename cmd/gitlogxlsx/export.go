package main

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/xuri/excelize/v2"
)

const (
	gitDateLayout    = "2006-01-02T15:04:05-07:00"
	defaultSheetName = "tRPC_Go需求导入模板"
)

type commitEntry struct {
	Hash    string
	Author  string
	Subject string
	Body    string
	Time    time.Time
}

type exportOptions struct {
	OutputPath string
	Category   string
	Estimate   float64
	DateFormat string
	SheetName  string
}

func collectGitCommits(repoPath, author, since, until string) ([]commitEntry, error) {
	if strings.TrimSpace(author) == "" {
		return nil, errors.New("author is required")
	}

	args := []string{}
	if repoPath != "" {
		args = append(args, "-C", repoPath)
	}

	args = append(args,
		"log",
		"--no-color",
		"--date=iso-strict",
		fmt.Sprintf("--author=%s", author),
		"--pretty=format:%H%x1f%an%x1f%ad%x1f%s%x1f%b%x1e",
	)

	if since != "" {
		args = append(args, fmt.Sprintf("--since=%s", since))
	}

	if until != "" {
		args = append(args, fmt.Sprintf("--until=%s", until))
	}

	cmd := exec.Command("git", args...)
	output, err := cmd.Output()
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			return nil, fmt.Errorf("git log failed: %s", strings.TrimSpace(string(exitErr.Stderr)))
		}
		return nil, fmt.Errorf("git log failed: %w", err)
	}

	return parseGitLogOutput(string(output))
}

func parseGitLogOutput(raw string) ([]commitEntry, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return []commitEntry{}, nil
	}

	chunks := strings.Split(raw, "\x1e")
	commits := make([]commitEntry, 0, len(chunks))

	for _, chunk := range chunks {
		chunk = strings.TrimSpace(chunk)
		if chunk == "" {
			continue
		}

		fields := strings.Split(chunk, "\x1f")
		if len(fields) < 5 {
			return nil, fmt.Errorf("unexpected git log record: %q", chunk)
		}

		parsedTime, err := time.Parse(gitDateLayout, strings.TrimSpace(fields[2]))
		if err != nil {
			return nil, fmt.Errorf("parse time for commit %s: %w", fields[0], err)
		}

		commits = append(commits, commitEntry{
			Hash:    strings.TrimSpace(fields[0]),
			Author:  strings.TrimSpace(fields[1]),
			Subject: strings.TrimSpace(fields[3]),
			Body:    strings.TrimSpace(fields[4]),
			Time:    parsedTime,
		})
	}

	return commits, nil
}

func exportXLSX(commits []commitEntry, opts exportOptions) error {
	if opts.OutputPath == "" {
		return errors.New("output path is required")
	}

	sheetName := opts.SheetName
	if sheetName == "" {
		sheetName = defaultSheetName
	}

	if err := os.MkdirAll(filepath.Dir(opts.OutputPath), 0o755); err != nil && !errors.Is(err, os.ErrExist) {
		return fmt.Errorf("create output directory: %w", err)
	}

	file := excelize.NewFile()
	defer func() {
		_ = file.Close()
	}()

	originalSheet := file.GetSheetName(0)
	if err := file.SetSheetName(originalSheet, sheetName); err != nil {
		return fmt.Errorf("rename sheet: %w", err)
	}

	if err := setColumnWidths(file, sheetName); err != nil {
		return err
	}

	header := []string{"标题", "需求类别", "处理人", "预估工时", "预计开始", "预计结束", "详细描述"}
	for idx, title := range header {
		cell, cellErr := excelize.CoordinatesToCellName(idx+1, 1)
		if cellErr != nil {
			return fmt.Errorf("convert header cell: %w", cellErr)
		}
		if err := file.SetCellValue(sheetName, cell, title); err != nil {
			return fmt.Errorf("write header %s: %w", cell, err)
		}
	}

	for i, commit := range commits {
		row := i + 2
		rowCells := map[string]any{
			fmt.Sprintf("A%d", row): safeSubject(commit),
			fmt.Sprintf("B%d", row): opts.Category,
			fmt.Sprintf("C%d", row): commit.Author,
			fmt.Sprintf("D%d", row): opts.Estimate,
			fmt.Sprintf("E%d", row): commit.Time.Format(opts.DateFormat),
			fmt.Sprintf("F%d", row): commit.Time.Format(opts.DateFormat),
			fmt.Sprintf("G%d", row): buildMessage(commit),
		}

		for cell, value := range rowCells {
			if err := file.SetCellValue(sheetName, cell, value); err != nil {
				return fmt.Errorf("write %s: %w", cell, err)
			}
		}
	}

	if err := file.SaveAs(opts.OutputPath); err != nil {
		return fmt.Errorf("save xlsx: %w", err)
	}

	return nil
}

func setColumnWidths(file *excelize.File, sheetName string) error {
	if err := file.SetColWidth(sheetName, "A", "A", 50); err != nil {
		return fmt.Errorf("set width for A: %w", err)
	}
	if err := file.SetColWidth(sheetName, "B", "D", 20); err != nil {
		return fmt.Errorf("set width for B-D: %w", err)
	}
	if err := file.SetColWidth(sheetName, "E", "F", 20); err != nil {
		return fmt.Errorf("set width for E-F: %w", err)
	}
	if err := file.SetColWidth(sheetName, "G", "G", 100); err != nil {
		return fmt.Errorf("set width for G: %w", err)
	}
	return nil
}

func safeSubject(commit commitEntry) string {
	subject := strings.TrimSpace(commit.Subject)
	if subject != "" {
		return subject
	}
	return commit.Hash
}

func buildMessage(commit commitEntry) string {
	subject := strings.TrimSpace(commit.Subject)
	body := strings.TrimSpace(commit.Body)
	if subject == "" {
		return body
	}

	if body == "" {
		return subject
	}

	return fmt.Sprintf("%s\n%s", subject, body)
}
