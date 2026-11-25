"""Execute Pipkin's bounded, profile-specific delivery graph."""

import json
import sys
from typing import TypedDict

from langgraph.graph import END, START, StateGraph


class Delivery(TypedDict):
    converged: bool


def stage_node(name: str):
    def execute(state: Delivery) -> Delivery:
        print(json.dumps({"stage": name}), flush=True)
        reply = sys.stdin.readline().strip()
        if reply not in {"ok", "stop"}:
            raise RuntimeError(f"Pipkin did not complete the {name} stage")
        return {"converged": state["converged"] or reply == "stop"}

    return execute


def main() -> None:
    stages = json.loads(sys.argv[1])
    gate = "validate_delivery"
    if not isinstance(stages, list) or not 2 <= len(stages) <= 7:
        raise ValueError("invalid number of delivery stages")
    if any(not isinstance(name, str) or not name.isidentifier() for name in stages):
        raise ValueError("invalid stage name")
    if len(set(stages)) != len(stages) or stages[-1] != gate:
        raise ValueError("delivery graph requires unique stages and a final archive gate")

    graph = StateGraph(Delivery)
    for name in stages:
        graph.add_node(name, stage_node(name))
    graph.add_edge(START, stages[0])
    graph.add_edge(gate, END)
    for name, next_stage in zip(stages, stages[1:]):
        if next_stage == gate:
            graph.add_edge(name, gate)
        else:
            graph.add_conditional_edges(
                name,
                lambda state, next_stage=next_stage: gate if state["converged"] else next_stage,
                [next_stage, gate],
            )
    graph.compile().invoke({"converged": False})


if __name__ == "__main__":
    main()
