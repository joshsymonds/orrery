// sendNow interrupts the turn, then says each text the interrupt cancelled again, in send order, each with a fresh
// prompt id. A refused or unanswered say stops it: that text and every later one are held, in order, for the box. An
// interrupt that rejects rejects sendNow, and nothing is said.
export async function sendNow(
  interrupt: () => Promise<string[]>,
  say: (text: string, promptId: string) => Promise<void>,
  newPromptId: () => string,
): Promise<{ held: string[] }> {
  const texts = await interrupt();
  for (const [index, text] of texts.entries()) {
    try {
      await say(text, newPromptId());
    } catch {
      return { held: texts.slice(index) };
    }
  }
  return { held: [] };
}
