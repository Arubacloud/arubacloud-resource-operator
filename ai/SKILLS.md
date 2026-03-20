# Skills

This file describes slash-command shortcuts that map to structured planning or implementation workflows.

---

## `/plan <new|refactor> controller for <Resource>`

**Trigger phrases:** any of the following should activate this workflow:
- `/plan new controller for <Resource>`
- `/plan refactor controller <Resource>`

**What to do:**

1. Read `ai/templates/plans/NEW_CONTROLLER.md` in full.
2. Read `ai/ARCHITECTURE.md` to recall the transition patterns, condition reason state machine, and component reuse catalogue.
3. Read the existing controller for `<Resource>` if one exists (`internal/controller/<resource>_controller.go`), and any associated CRD type (`api/v1alpha1/<resource>_types.go`).
4. Produce a filled-in plan by substituting every `<…>` placeholder with resource-specific content derived from the code and the CMP API. Do not include the template instructions or generic guidance in the output — only the completed plan.
5. **Save the plan** to `ai/plans/<type>_controller_for_<resource>.md`, where `<type>` is either `new` or `refactor` depending on §0 of the plan, and `<resource>` is the lowercase resource name (e.g. `ai/plans/refactor_controller_for_vpc.md`). This step is mandatory — do not skip it.
6. Present the plan to the developer for review before writing any code.
