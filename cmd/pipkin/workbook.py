"""Read a workbook summary or apply explicit cell edits to a separate copy."""
import datetime
import json
import re
import sys
import warnings
from zipfile import ZipFile

try:
    import openpyxl
    from openpyxl.utils.cell import coordinate_to_tuple
except ImportError:
    sys.exit("XLSX needs openpyxl. Run make install-xlsx, or set PIPKIN_XLSX_PYTHON to a Python environment with openpyxl >= 3.1.")

source = sys.argv[1]
operation = sys.argv[2] if len(sys.argv) > 2 else None
destination = operation if operation not in ("--sheets", "--grid") else None


def serialise(value):
    if isinstance(value, (datetime.date, datetime.time)):
        return value.isoformat()
    if isinstance(value, datetime.timedelta):
        return str(value)
    return value if isinstance(value, (int, float, bool, str)) or value is None else str(value)


def preview_value(value):
    value = serialise(value)
    return value[:300] + "…" if isinstance(value, str) and len(value) > 300 else value


def cell_value(edit):
    value = edit["value"]
    kind = edit.get("kind", "value")
    if kind == "formula":
        if not isinstance(value, str) or not value.startswith("=") or len(value) > 8192:
            raise ValueError("formula must begin with = and fit in 8192 characters")
    elif kind != "value" or (value is not None and type(value) not in (str, int, float, bool)):
        raise ValueError("value must be a JSON scalar, or kind=formula")
    elif isinstance(value, str) and len(value) > 32767:
        raise ValueError("cell text exceeds 32767 characters")
    return value, kind


try:
    with ZipFile(source) as archive:
        entries = archive.infolist()
        if len(entries) > 4000 or sum(entry.file_size for entry in entries) > 64 * 1024**2:
            raise ValueError("workbook exceeds the supported ZIP size")
        if destination and any(
            entry.filename.startswith(("xl/activeX/", "xl/embeddings/", "xl/slicers/", "_xmlsignatures/"))
            or entry.filename.lower().endswith("vbaproject.bin")
            for entry in entries
        ):
            raise ValueError("workbook contains advanced features that cannot be preserved")
    if destination:
        warnings.filterwarnings("error", category=UserWarning, module=r"openpyxl\..*")
    # Older distro builds can read XLSX. Keep rich text preservation mandatory for writes.
    from inspect import signature

    options = {}
    if "rich_text" in signature(openpyxl.load_workbook).parameters:
        options["rich_text"] = True
    elif destination:
        raise ValueError("XLSX editing requires openpyxl >= 3.1. Run make install-xlsx.")
    workbook = openpyxl.load_workbook(source, read_only=not destination, data_only=False, **options)
    if operation == "--sheets":
        print(json.dumps(workbook.sheetnames))
    elif operation == "--grid":
        sheet = workbook[sys.argv[3]]
        if not hasattr(sheet, "iter_rows"):
            raise ValueError("this sheet does not contain an editable cell grid")
        if not sheet.max_row or not sheet.max_column:
            sheet.calculate_dimension(force=True)
        max_row, max_column = max(1, sheet.max_row or 1), max(1, sheet.max_column or 1)
        if max_row * max_column > 200_000:
            raise ValueError("worksheet exceeds the 200,000-cell terminal grid limit")
        rows = []
        text_size = 0
        for row in sheet.iter_rows(min_row=1, max_row=max_row, min_col=1, max_col=max_column, values_only=True):
            values = ["" if cell is None else str(serialise(cell)) for cell in row]
            text_size += sum(len(value) for value in values)
            if text_size > 8 * 1024**2:
                raise ValueError("worksheet text exceeds the terminal grid limit")
            rows.append(values)
        print(json.dumps(rows))
    elif not destination:
        sheets = []
        for sheet in workbook.worksheets[:20]:
            preview = [[preview_value(cell) for cell in row] for row in
                       sheet.iter_rows(min_row=1, max_row=min(sheet.max_row or 20, 20),
                                       min_col=1, max_col=min(sheet.max_column or 12, 12), values_only=True)]
            sheets.append({"name": sheet.title, "rows": sheet.max_row, "columns": sheet.max_column,
                           "preview": preview})
        print(json.dumps({"sheets": sheets, "sheet_count": len(workbook.worksheets)}, allow_nan=False))
    else:
        edits = json.load(sys.stdin)
        if not isinstance(edits, list) or not 1 <= len(edits) <= 1000:
            raise ValueError("edits must be a JSON array of 1 to 1000 cells")
        seen = set()
        changes = []
        for edit in edits:
            sheet, address = edit["sheet"], edit["cell"].upper()
            if not re.fullmatch(r"[A-Z]{1,3}[1-9][0-9]{0,6}", address):
                raise ValueError(f"invalid cell {address}")
            row, column = coordinate_to_tuple(address)
            if row > 1048576 or column > 16384 or (sheet, address) in seen:
                raise ValueError(f"invalid or duplicate cell {sheet}!{address}")
            seen.add((sheet, address))
            value, kind = cell_value(edit)
            cell = workbook[sheet][address]
            before, before_type = serialise(cell.value), cell.data_type
            cell.value = value
            if kind == "value" and isinstance(value, str):
                cell.data_type = "s"
            changes.append({"sheet": sheet, "cell": address, "before": before, "before_type": before_type,
                            "after": serialise(cell.value), "after_type": cell.data_type})
        workbook.save(destination)
        print(json.dumps({"changes": changes}, allow_nan=False))
except Exception as exc:
    sys.exit(f"Workbook: {exc}")
