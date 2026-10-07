# purge fixtures

Source trees used by `internal/purge` tests. Tests copy a case into a temp
directory and, where the case needs a repository, initialize git (or plant a
`.git` marker) in the copy. No fixture contains a committed `.git` directory, so
the outer repository never sees an embedded repo.

- `tracked/build/` — a build artifact that the test adds to a temp git index;
  discovery must keep it.
- `untracked/node_modules/` — an artifact outside any repository; discovery must
  offer it for purge.
- `nestedrepo/build/` — an artifact that contains a nested repository; discovery
  must keep it.