package main

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/xuri/excelize/v2"
)

func TestParseGitLogOutput(t *testing.T) {
	raw := "hash1\x1fAlice\x1f2024-10-20T10:11:12+08:00\x1ffeat: add api\x1fdetail line\x1e" +
		"hash2\x1fBob\x1f2024-10-21T00:00:00+00:00\x1ffix: bug\x1f\x1e"

	commits, err := parseGitLogOutput(raw)
	assert.NoError(t, err)
	assert.Len(t, commits, 2)

	assert.Equal(t, "hash1", commits[0].Hash)
	assert.Equal(t, "Alice", commits[0].Author)
	assert.Equal(t, "feat: add api", commits[0].Subject)
	assert.Equal(t, "detail line", commits[0].Body)
	expectedLocal := time.Date(2024, 10, 20, 10, 11, 12, 0, time.FixedZone("UTC+8", 8*3600))
	assert.True(t, commits[0].Time.Equal(expectedLocal))

	assert.Equal(t, "hash2", commits[1].Hash)
	assert.Equal(t, "Bob", commits[1].Author)
	assert.Equal(t, "fix: bug", commits[1].Subject)
	assert.Equal(t, "", commits[1].Body)
	expectedUTC := time.Date(2024, 10, 21, 0, 0, 0, 0, time.UTC)
	assert.True(t, commits[1].Time.Equal(expectedUTC))
}

func TestExportXLSX(t *testing.T) {
	tempDir := t.TempDir()
	output := filepath.Join(tempDir, "commits.xlsx")

	commits := []commitEntry{
		{
			Hash:    "abc123",
			Author:  "alice",
			Subject: "feat: add api",
			Body:    "more detail",
			Time:    time.Date(2024, 10, 20, 10, 11, 12, 0, time.FixedZone("UTC+8", 8*3600)),
		},
	}

	opts := exportOptions{
		OutputPath: output,
		Category:   "MF",
		Estimate:   0.75,
		DateFormat: "2006-01-02",
		SheetName:  defaultSheetName,
	}

	err := exportXLSX(commits, opts)
	assert.NoError(t, err)

	file, openErr := excelize.OpenFile(output)
	assert.NoError(t, openErr)
	defer func() {
		_ = file.Close()
	}()

	val, err := file.GetCellValue(defaultSheetName, "A1")
	assert.NoError(t, err)
	assert.Equal(t, "标题", val)

	val, err = file.GetCellValue(defaultSheetName, "A2")
	assert.NoError(t, err)
	assert.Equal(t, "feat: add api", val)

	val, err = file.GetCellValue(defaultSheetName, "B2")
	assert.NoError(t, err)
	assert.Equal(t, "MF", val)

	val, err = file.GetCellValue(defaultSheetName, "C2")
	assert.NoError(t, err)
	assert.Equal(t, "alice", val)

	val, err = file.GetCellValue(defaultSheetName, "D2")
	assert.NoError(t, err)
	assert.Equal(t, "0.75", val)

	val, err = file.GetCellValue(defaultSheetName, "E2")
	assert.NoError(t, err)
	assert.Equal(t, "2024-10-20", val)

	val, err = file.GetCellValue(defaultSheetName, "F2")
	assert.NoError(t, err)
	assert.Equal(t, "2024-10-20", val)

	val, err = file.GetCellValue(defaultSheetName, "G2")
	assert.NoError(t, err)
	assert.Equal(t, "feat: add api\nmore detail", val)
}
