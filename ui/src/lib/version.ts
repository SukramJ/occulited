/**
 * occulited's version as the Status page shows it (task 133; again since occulited task 16). A build
 * from a commit is named by the commit - 40 hex digits and an optional suffix (`-hot` for a binary deployed onto a system by hand) -
 * and the page shows the first nine digits with the suffix kept. Anything else (`stub`, a branch
 * build such as `t66-ecafbd00`) stays as it is.
 */
export function shortVersion(v: string): string {
    const m = /^([0-9a-f]{40})(.*)$/.exec(v);
    return m ? m[1]!.slice(0, 9) + m[2]! : v;
}

