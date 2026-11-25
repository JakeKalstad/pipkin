"""Schedule Pipkin delivery stages with bounded LangGraph transitions."""

import json
import sys
from typing import TypedDict

from langgraph.graph import END, START, StateGraph


class Delivery(TypedDict):
    reviews: int
    converged: bool


def run_stage(stage: str) -> bool:
    print(json.dumps({"stage": stage}), flush=True)
    reply = sys.stdin.readline().strip()
    if reply not in {"ok", "stop"}:
        raise RuntimeError(f"Pipkin did not complete the {stage} stage")
    return reply == "stop"


def draft(state: Delivery) -> Delivery:
    run_stage("draft")
    return {"reviews": 0, "converged": False}


def review(state: Delivery) -> Delivery:
    return {"converged": run_stage("review"), "reviews": state["reviews"] + 1}


def verify(state: Delivery) -> Delivery:
    run_stage("verify")
    return {"converged": False, "reviews": state["reviews"] + 1}


def main() -> None:
    passes = int(sys.argv[1])
    verification = sys.argv[2] == "true"
    graph = StateGraph(Delivery)
    graph.add_node("draft", draft)
    graph.add_node("review", review)
    graph.add_node("verify", verify)
    def plan(state: Delivery) -> Delivery:
        run_stage("plan")
        return state

    if sys.argv[3] == "true":
        graph.add_node("plan", plan)
        graph.add_edge(START, "plan")
        graph.add_edge("plan", "draft")
    else:
        graph.add_edge(START, "draft")
    graph.add_edge("draft", "review")
    graph.add_edge("verify", END)

    def after_review(state: Delivery) -> str:
        if state["converged"] or state["reviews"] >= passes:
            return END
        if verification and state["reviews"] == passes - 1:
            return "verify"
        return "review"

    graph.add_conditional_edges(
        "review", after_review, {"review": "review", "verify": "verify", END: END}
    )
    graph.compile().invoke({"reviews": 0, "converged": False})


if __name__ == "__main__":
    main()
