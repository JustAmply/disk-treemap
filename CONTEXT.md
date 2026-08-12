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

## Folder View boundaries

- The server owns path validation, filters, result limits, summaries, and the maximum treemap expansion budget.
- The browser may group small nodes after layout according to the current viewport. This presentation-only grouping must not change the server summary.
- `/children` and `/largest` remain compatibility endpoints. They use the same path and filter rules as the Folder View.
