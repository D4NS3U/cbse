# Using the implementation prompts

Use [`00-sequential-controller.md`](00-sequential-controller.md) for the normal implementation flow. The controller finds the earliest incomplete stable ID, implements one slice by default, and may implement only the immediately following slice when the first slice passes its full gate.

Do not combine the controller with a numbered launcher in one run. Use a numbered launcher only when the target slice is already known and every earlier slice has verified evidence in [`../IMPLEMENTATION_HANDOFF.md`](../IMPLEMENTATION_HANDOFF.md). A numbered launcher implements one slice only.

For a normal run, give the coding agent this instruction:

```text
Follow devlog/changes/simrunner-start-and-container-creation/prompts/00-sequential-controller.md.

Run budget: one slice.
Use devlog/changes/simrunner-start-and-container-creation/IMPLEMENTATION_HANDOFF.md as routing evidence and update it before your final response.
```

Keep the same worktree between runs. Review each handoff and create a human-controlled checkpoint commit after accepting a completed slice; the coding-agent prompts do not authorize commits. A later agent validates the recorded revision, relevant diff, stable IDs, and test evidence instead of accepting the handoff as proof.

If mandatory smoke verification is blocked, keep the same revision and worktree, run the required smoke command externally with the protected input already supplied through the environment, and record only the sanitized result in the handoff. Do not advance to the next slice until the required smoke tier passes.
