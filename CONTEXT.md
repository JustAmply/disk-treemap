# Disk Treemap Storage Exploration

Disk Treemap scans one configured directory tree and presents durable storage snapshots for inspection.

## Language

**Scan Run**:
One requested traversal of the configured directory tree, from queued work to a terminal outcome.
_Avoid_: Scan job, scan process

**Scan Snapshot**:
The complete, durable directory tree produced by a successful Scan Run. Partial or failed runs are not Scan Snapshots.
_Avoid_: Scan data, result set

**Folder View**:
A bounded view of one directory inside a Scan Snapshot, including its visible contents and size summary.
_Avoid_: Explore result, children response
