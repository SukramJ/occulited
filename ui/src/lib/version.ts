/**
 * occulited's version as the Status page shows it (task 133). A build from a commit is named by the
 * commit - 40 hex digits and an optional suffix (`-hot` for a binary deployed onto a system by hand) -
 * and the page shows the first nine digits with the suffix kept. Anything else (`stub`, a branch
 * build such as `t66-ecafbd00`) stays as it is.
 */
export function shortVersion(v: string): string {
    const m = /^([0-9a-f]{40})(.*)$/.exec(v);
    return m ? m[1]!.slice(0, 9) + m[2]! : v;
}

/**
 * occulited's version with its commit beside it (task 9): `1.0.0-dev.38 (fa42dfde1)`. The version
 * is the image version occulited's commit is tagged with, or `git describe`'s
 * `1.0.0-dev.38-5-gfa42dfd` between build rounds - that one names the commit already, so it stands
 * alone. An older build's version is the commit itself (shortVersion).
 */
export function occulitedVersion(version: string, commit?: string): string {
    const v = shortVersion(version);
    if (!commit || v !== version) return v;
    const described = /-g([0-9a-f]+)(?:-dirty)?$/.exec(version);
    if (described && commit.startsWith(described[1]!)) return version;
    return `${version} (${commit.slice(0, 9)})`;
}
