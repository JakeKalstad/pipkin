"""Schedule Pipkin's draft and review operations with LangGraph."""

import json
import sys
from typing import TypedDict

from langgraph.graph import END, START, StateGraph


class Delivery(TypedDict):
    reviews: int


def run_stage(stage: str) -> None:
    print(json.dumps({"stage": stage}), flush=True)
    if sys.stdin.readline().strip() != "ok":
        raise RuntimeError(f"Pipkin did not complete the {stage} stage")


def draft(state: Delivery) -> Delivery:
    run_stage("draft")
    return {"reviews": 0}


def review(state: Delivery) -> Delivery:
    run_stage("review")
    return {"reviews": state["reviews"] + 1}


def main() -> None:
    review_passes = int(sys.argv[1])
    graph = StateGraph(Delivery)
    graph.add_node("draft", draft)
    graph.add_node("review", review)
    graph.add_edge(START, "draft")
    graph.add_edge("draft", "review")
    graph.add_conditional_edges(
        "review",
        lambda state: "review" if state["reviews"] < review_passes else END,
        {"review": "review", END: END},
    )
    graph.compile().invoke({"reviews": 0})


if __name__ == "__main__":
    main()
