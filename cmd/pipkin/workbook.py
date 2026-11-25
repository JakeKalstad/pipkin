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
    sys.exit("Install openpyxl in the Python environment used by PIPKIN_XLSX_PYTHON.")

source = sys.argv[1]
destination = sys.argv[2] if len(sys.argv) > 2 else None


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
    workbook = openpyxl.load_workbook(source, read_only=not destination, data_only=False, rich_text=True)
    if not destination:
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
