# LAZY.md — Be lazy the smart way

We **love** issues — thank you!  
We **love even more** when people send a Pull Request (especially together with / instead of just an issue).

The happiest path for everyone: **you do the work → we review & merge quickly**.

## Why we strongly prefer PRs over long issue discussions

- You keep your momentum instead of waiting for "maybe" feedback
- We see your **actual intended solution** (saves tons of back-and-forth)
- Small, focused PRs get reviewed & merged much faster than prose
- "Show, don't tell" almost always leads to better outcomes

### The laziest (and most appreciated) contribution flow

Because this is an open repo, external contributors **cannot push branches directly** — so the flow uses your own fork:

1. **Fork** this repository to your GitHub account (click **Fork** button — takes 2 seconds)
2. **Clone** your fork locally  
   `git clone https://github.com/YOUR-USERNAME/REPO-NAME.git`
3. Create a **descriptive branch**  
   `git checkout -b fix/typo-in-readme` or `git checkout -b feat/add-dark-mode-toggle`
4. Make your changes, commit them (good messages help!)
5. **Push** the branch to **your fork**  
   `git push origin fix/typo-in-readme`
6. Go to **your fork** on GitHub → click **Compare & pull request** (or go to the original repo and start a PR from there)
7. In the PR:
    - Clear title ("Fix typo in README installation section")
    - In the body:
        - What problem does this solve? (link the issue if it exists: `Fixes #123`)
        - Brief before/after or screenshot if visual
        - "I opened this because I was already working on it and thought it might save time"
8. *(optional but nice)* If no issue existed yet → create a quick one afterward and link your PR in it

→ We review code **way faster** than long issue threads.  
→ Even draft PRs / early PoCs are welcome — just mark them as draft.

### TL;DR — sentences that make maintainers smile

> "I already had a fix ready, so I forked → branched → pushed → opened this PR. Fixes #42."
>
> "Started on this yesterday — here's a working PoC. Happy to iterate!"

That energy usually gets your contribution landed quickly ❤️

### Bonus: even lazier next time

- After your first PR lands, you can keep your fork around and
  `git remote add upstream https://github.com/ORIGINAL-OWNER/REPO.git` so you can easily pull latest changes
  (`git fetch upstream && git rebase upstream/main`)
- GitHub's "Open pull request" button from an issue page will auto-suggest forking if you haven't already

Thank you for being **productively lazy** — it keeps this project moving!

Happy hacking!
