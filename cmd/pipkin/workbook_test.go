package main

import (
	"context"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestWorkbookCopyAndPreview(t *testing.T) {
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("Python unavailable")
	}
	if err := exec.Command(python, "-I", "-c", "import openpyxl").Run(); err != nil {
		t.Skip("openpyxl unavailable")
	}
	t.Setenv("PIPKIN_XLSX_PYTHON", python)
	root := t.TempDir()
	source := filepath.Join(root, "budget.xlsx")
	makeWorkbook := `
import openpyxl, sys
from openpyxl.styles import Font
w = openpyxl.Workbook()
s = w.active
s.title = "Budget"
s["B2"] = 100
s["B2"].font = Font(bold=True)
w.save(sys.argv[1])
`
	if out, err := exec.Command(python, "-I", "-c", makeWorkbook, source).CombinedOutput(); err != nil {
		t.Fatalf("make workbook: %v %s", err, out)
	}
	preview, err := workbookOperation(context.Background(), root, "budget.xlsx", "", nil)
	if err != nil || !strings.Contains(string(preview), "Budget") {
		t.Fatalf("preview: %s %v", preview, err)
	}
	edits := []byte(`[{"sheet":"Budget","cell":"B2","value":250},{"sheet":"Budget","cell":"B3","kind":"formula","value":"=B2*2"},{"sheet":"Budget","cell":"B4","value":"=literal"}]`)
	diff, err := workbookOperation(context.Background(), root, "budget.xlsx", "copy.xlsx", edits)
	if err != nil || !strings.Contains(string(diff), `"before": 100`) {
		t.Fatalf("edit: %s %v", diff, err)
	}
	check := `
import openpyxl, sys
before, after = [openpyxl.load_workbook(p)["Budget"] for p in sys.argv[1:]]
assert before["B2"].value == 100
assert after["B2"].value == 250 and after["B2"].font.bold
assert after["B3"].value == "=B2*2" and after["B3"].data_type == "f"
assert after["B4"].value == "=literal" and after["B4"].data_type == "s"
`
	if out, err := exec.Command(python, "-I", "-c", check, source, filepath.Join(root, "copy.xlsx")).CombinedOutput(); err != nil {
		t.Fatalf("check workbook: %v %s", err, out)
	}
	if _, err := workbookOperation(context.Background(), root, "budget.xlsx", "budget.xlsx", edits); err == nil {
		t.Fatal("expected source overwrite to fail")
	}
}
