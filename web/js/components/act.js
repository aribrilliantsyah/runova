import { toast } from "/assets/js/toast.js";

/**
 * act(button, fn, okMessage) runs fn with the button disabled and marked busy; an error becomes a
 * toast and the button is restored. Returns fn's result, or undefined after a failure.
 */
export async function act(button, fn, okMessage) {
  if (button) {
    button.disabled = true;
    button.setAttribute("aria-busy", "true");
  }
  try {
    const out = await fn();
    if (okMessage) toast(okMessage);
    return out ?? true;
  } catch (e) {
    toast(e.message, "err");
    return undefined;
  } finally {
    if (button) {
      button.disabled = false;
      button.removeAttribute("aria-busy");
    }
  }
}
