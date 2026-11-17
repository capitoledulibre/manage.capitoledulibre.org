# Documentation

The specifications and decisions behind manage. The user and operator
documentation stays in the top-level [README](../README.md) for now.

## Conventions

- One document per plan. Each starts with a **status**: *record* (what is
  built), *draft* (proposed, not validated) or *validated on <date>*, with
  the lots delivered so far.
- Decisions carry a `D` number in **one sequence across every document**:
  a new plan says which number it starts from. A decision is a row of a
  table — subject, decision, why — and is never renumbered; a decision
  that changes is superseded by a new one that says so.
- Product lots carry an `L` number, in one sequence too. A lot names the
  decisions it builds and ends with an **acceptance** criterion: what one
  can do, or see, once it is delivered.
- A validated document records what was decided. Notes learnt while
  delivering a lot are added under its status, not rewritten into the
  decisions.

## Plans

- [Platform](platform.md) — what manage is built on today: the
  decisions behind the partners and volunteers spaces (D1-D15), a record
- [The whole event](event-plan.md) — CFP and program, ticketing,
  check-in and badges, exhibitors, code of conduct, expenses, feedback:
  replacing pretalx and pretix (D16-D45, L1-L24), draft
