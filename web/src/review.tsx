import type { JSX } from "preact";

import type { ReviewView } from "./ending";
import { Icon } from "./glyphs";

// EpicReview draws an epic's Review section, headed Review: its count words, the reviewer table with each reviewer's reason
// and verdict, and the findings table with each finding's id, what and where, and its result. Under each finding row its
// reason and its closures by round are drawn when there are any. It decides nothing: the view gives the words and tones.
export function EpicReview(props: { view: ReviewView }): JSX.Element {
  const { view } = props;
  return (
    <section class="review" aria-label="Review">
      <div class="sec" data-hue="2">
        <Icon name="search" />
        <span class="name">Review</span>
        <span class="n">{view.words}</span>
      </div>
      {view.reviewers.length > 0 && (
        <div class="tbl reviews">
          {view.reviewers.map((reviewer) => (
            <div class="tr" data-reviewer={reviewer.reviewer}>
              <span class="strong">{reviewer.reviewer}</span>
              <span class="wide">{reviewer.reason}</span>
              <span class="end">
                <span class={`chip ${reviewer.tone}`}>{reviewer.verdict}</span>
              </span>
            </div>
          ))}
        </div>
      )}
      {view.findings.length > 0 && (
        <div class="tbl findings">
          {view.findings.map((finding) => (
            <>
              <div class="tr" data-finding={finding.id}>
                <span class="id">{finding.id}</span>
                <span class="wide">
                  {finding.what}
                  {finding.where !== "" && <span class="where"> · {finding.where}</span>}
                </span>
                <span class="end">
                  <span class={`chip ${finding.tone}`}>{finding.result}</span>
                </span>
              </div>
              {(finding.reason !== "" || finding.closures.length > 0) && (
                <div class="tr-detail">
                  {finding.reason !== "" && <p class="reason">{finding.reason}</p>}
                  {finding.closures.map((closure) => (
                    <p class={`closure ${closure.tone}`}>{closure.text}</p>
                  ))}
                </div>
              )}
            </>
          ))}
        </div>
      )}
    </section>
  );
}
