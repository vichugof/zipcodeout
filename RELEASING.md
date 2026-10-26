# Releasing zipcode

This guide covers how a release is made, what the binaries in it are, and how
to download and verify them.

## Making a release

1. Check that the latest CI run on `main` passed.
2. Choose the next version. Versions follow semantic versioning,
   `vMAJOR.MINOR.PATCH`. While the major version is 0, flags and output can
   still change between minor versions.
3. Tag the commit and push the tag:

   ```sh
   git tag -a v0.2.0 -m "zipcode v0.2.0"
   git push origin v0.2.0
   ```

Pushing the tag starts the `release` workflow, which does everything else in a
few minutes. Follow it under the repository's **Actions** tab, or with
`gh run watch`. The finished release appears under **Releases**.

## What the release workflow does

`.github/workflows/release.yml` runs whenever a tag starting with `v` is
pushed. Branch pushes run `ci.yml` instead.

1. **Test on Linux and Windows.** The `test` job runs the unit and end-to-end
   tests of the tagged commit on `ubuntu-latest` and `windows-latest`. The
   end-to-end tests build the real binary and run it, so on Windows they run
   `zipcode.exe` itself. If either system fails, nothing is published.
2. **Build.** The `release` job runs `make package VERSION=<tag>` on Linux.
   This first runs `make cross`, which compiles the four binaries on that one
   Linux machine. The Go compiler builds for other systems directly, so no
   Windows machine is needed to produce the `.exe` files.

   | Build setting | What it does |
   |---|---|
   | `GOOS`, `GOARCH` | Choose the target system (`linux`, `windows`) and processor (`amd64`, `arm64`) |
   | `CGO_ENABLED=0` | Builds pure Go code that is statically linked. The binary contains everything it needs, so it runs without any libraries or a Go installation. |
   | `-trimpath` | Leaves the build machine's file paths out of the binary |
   | `-ldflags "-s -w"` | Drops the symbol table and debug information, which makes the binary about 30% smaller |
   | `-ldflags "-X main.version=<tag>"` | Stamps the version, which `zipcode -version` prints |

3. **Package.** Each binary goes into an archive together with the README:
   - **Linux:** `zipcode_<version>_linux_<arch>.tar.gz`. A tar archive keeps
     the file's executable permission, so the binary runs as soon as it is
     extracted.
   - **Windows:** `zipcode_<version>_windows_<arch>.zip`. Windows opens zip
     files without extra software.
   - **Checksums:** `SHA256SUMS` lists the SHA-256 hash of each archive, so a
     download can be checked for corruption or tampering.
4. **Publish.** `gh release create` creates the release:
   - It is titled `zipcode <version>` and has the five files attached.
   - Its notes are generated from the changes since the previous release
     (`--generate-notes`).
   - It is published only if the tag exists on GitHub (`--verify-tag`).
   - The workflow's token can write releases only in this job (`permissions:
     contents: write`); everywhere else it is read-only.

GitHub also attaches **Source code (zip)** and **Source code (tar.gz)** to every
release. Those are snapshots of the repository at the tag, not binaries.

## The files in a release

| File | Use it on |
|---|---|
| `zipcode_<version>_linux_amd64.tar.gz` | Linux on 64-bit Intel or AMD processors: most PCs, servers and cloud machines |
| `zipcode_<version>_linux_arm64.tar.gz` | Linux on 64-bit ARM processors: Raspberry Pi 4 or 5 with a 64-bit system, AWS Graviton and other ARM servers |
| `zipcode_<version>_windows_amd64.zip` | Windows 10 or 11 on Intel or AMD processors: most PCs |
| `zipcode_<version>_windows_arm64.zip` | Windows 11 on ARM processors, such as Snapdragon laptops |
| `SHA256SUMS` | Checking the downloads |

Each binary is about 6 MB, or 2.5 MB compressed, and contains the Go runtime
and the whole tool. The only thing it uses from the system is the list of
trusted certificates for HTTPS connections:
- **Linux:** `/etc/ssl/certs`, which every normal installation has. Minimal
  container images need the `ca-certificates` package.
- **Windows:** the system certificate store.

## Downloading

The repository is private, so its releases are private too. Only GitHub
accounts with access to the repository can see the release page or download
the files:
- **Browser:** sign in to GitHub and open **Releases** on the repository page.
- **Terminal:** `gh release download` uses your `gh` login. Without a tag
  argument, it downloads from the latest release.

### Linux

```sh
gh release download --repo vichugof/zipcodeout --pattern '*_linux_amd64.tar.gz' --pattern SHA256SUMS
sha256sum --check --ignore-missing SHA256SUMS   # prints "<file>: OK"
tar -xzf zipcode_*_linux_amd64.tar.gz zipcode
./zipcode -version
sudo install zipcode /usr/local/bin/            # optional: make it available everywhere
```

### Windows (PowerShell)

```powershell
gh release download --repo vichugof/zipcodeout --pattern '*_windows_amd64.zip' --pattern SHA256SUMS
$archive = Get-Item .\zipcode_*_windows_amd64.zip
$expected = (Select-String -Path .\SHA256SUMS -Pattern $archive.Name -SimpleMatch).Line.Split(' ')[0]
(Get-FileHash $archive -Algorithm SHA256).Hash -eq $expected   # True if the download is intact
$archive | Expand-Archive -DestinationPath .\zipcode
.\zipcode\zipcode.exe -version
```

### Windows SmartScreen

The binary isn't code-signed. When it is downloaded with a browser, Windows
marks it as coming from the internet, and the first run can show **Windows
protected your PC**. There are two ways past the warning:
- Choose **More info**, then **Run anyway**.
- Remove the mark before running: `Unblock-File .\zipcode\zipcode.exe`.

Files downloaded with `gh` are not marked. Signing the binary with a
code-signing certificate would remove the warning for everyone.

## When something goes wrong

- **The tests fail in the release workflow.** Nothing is published. Fix the
  problem on `main`, then move the tag to the fixed commit:

  ```sh
  git push --delete origin v0.2.0
  git tag -d v0.2.0
  git tag -a v0.2.0 -m "zipcode v0.2.0"   # run on the fixed commit
  git push origin v0.2.0
  ```

- **A published release is broken.** Don't move its tag, because someone may
  already have downloaded it. Publish a patch version such as `v0.2.1`
  instead. Then either mark the broken release as a pre-release or delete it
  with `gh release delete v0.2.0`.
- **The publish step fails after creating the release**, for example during
  an upload. Delete the incomplete release with `gh release delete v0.2.0`,
  which keeps the tag, then re-run the failed job from the **Actions** tab.

## Packaging locally

`make package` builds the same archives into `dist/release/`:

```sh
make package VERSION=v0.2.0
```

Without `VERSION`, the version comes from `git describe`: the tag on the
current commit, or else the commit's short hash.
