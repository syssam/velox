# 0169. A directory for a plugin's files that are not Go

- Status: accepted, 2026-11-24. Builds on 0097 (package generate) and 0125
  (`sdlfiles=entity`).

## Context

An application runs `velox-gen -out veloxgen -plugin
velox-graphql:sdlfiles=entity` and gets `velox.graphql` and 478
`velox_<entity>.graphql` files in `veloxgen/`. gqlgen reads its SDL from
`schema/`. A plugin's path had to stay below `-out`, so the application
copied the files, and `-check` then could not see the copies. veloxe had
`graphqlgen.WithSchemaPath("schema/velox.graphql")`.

## Decision

- velox-gen takes `-files dir`, for generate, `-check` and verify, and
  package generate takes `Config.Files`. With it, each plugin file that is
  not Go (`.graphql`, `.proto`) is written below dir, at the path it would
  have below `-out`. A `.go` file stays in `-out`, since it is in the
  package. Without it nothing changes. A plugin does not see the flag; the
  protocol is unchanged.
- dir is resolved as `-out` is, relative to the working directory, then
  through its symbolic links. It must be strictly inside the module that
  holds `-out` (the nearest go.mod above it), and not:
  - the module's root, nor any directory above `-out`, whose sweep would
    reach `-out`'s tree;
  - inside `-out`, which `-out`'s own sweep owns;
  - below a directory that holds a go.mod or velox-gen's `velox.go`: the
    root of another module or another run's output, whose sweep would
    reach it.
  So `../x`, an absolute path outside the module and a symbolic link that
  leads out of it are refused, before anything runs. dir equal to `-out` is
  as without the flag.
- velox-gen owns, below dir, exactly the `.graphql` and `.proto` files that
  start with its header. `-check` reports one that is missing, out of date
  or no longer written. A run writes them and removes the stale ones and
  the directories that leaves empty, but never dir itself, a file without
  the header, whatever its name, or a `.go` file. The sweep skips the
  directories `-out`'s sweep skips. A file without the header at a path a
  plugin writes is refused, as in `-out`.
- With `-files`, `-out`'s sweep is unchanged, so the `.graphql` files a run
  without it left in `-out` are stale there and removed: adding the flag
  moves the files. velox-gen sweeps only the directories it is given, so
  after dropping or changing `-files` the old directory's files are deleted
  by hand.
- One write covers `-out` and dir, below the directory that holds both, so
  the run stays all or nothing.
- verify takes `-files` and checks it. Its program compiles only the
  package, whose files stay in `-out`, so nothing else changes for it.
- The command `-check` prints to rerun carries `-files`.
- velox-gen reads no configuration file for generate, so the setting is the
  flag and `Config.Files` alone.

## Consequences

- An application keeps its SDL where gqlgen reads it, and `-check` covers
  it, with no copy.
- A directory beside `-out` holds files of two owners: velox-gen's, marked
  by the header, and the application's, which velox-gen never touches.
- Rejected: a per-plugin option (`velox-graphql:sdldir=`), which would make
  each plugin reach outside `-out` and velox-gen check paths it cannot see;
  moving `.go` files too, which belong to the package; letting dir be
  above or inside `-out`, which would give a file two sweeps.
