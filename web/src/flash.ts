import { useCallback, useEffect, useRef, useState } from "preact/hooks";

// flashMillis is how long a used control stays flashed; the phone's TapFeedback uses the same window.
export const flashMillis = 300;

// useFlash returns whether a control is flashing and a function that starts a flash. A use during a flash
// restarts the window, and unmounting clears the pending timer so no state is set after it.
export function useFlash(): [boolean, () => void] {
  const [flashing, setFlashing] = useState(false);
  const timer = useRef<ReturnType<typeof setTimeout>>();

  useEffect(() => () => clearTimeout(timer.current), []);

  const flash = useCallback(() => {
    clearTimeout(timer.current);
    setFlashing(true);
    timer.current = setTimeout(() => setFlashing(false), flashMillis);
  }, []);

  return [flashing, flash];
}
