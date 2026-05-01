import { describe, it, expect, vi } from "vitest";
import { render, screen, fireEvent } from "@testing-library/react";
import { MerchantDefaultCategoryDialog } from "@/components/classification/merchant-default-category-dialog";

// Specs for the cascade-confirmation dialog. Covers:
//   - hidden when open=false
//   - copy variant when oldCategoryName is null (first-time default)
//   - copy variant when both old + new are set
//   - apply / skip / cancel callbacks (button + Esc)
//
// Note: Radix Dialog portals its content to document.body, so queries use
// `screen` rather than the container returned by render(). Backdrop-click
// dismissal is exercised by Radix's own test suite — we cover Esc here.

function renderDialog(
  overrides: Partial<React.ComponentProps<typeof MerchantDefaultCategoryDialog>> = {}
) {
  const onApply = vi.fn();
  const onSkip = vi.fn();
  const onCancel = vi.fn();
  const utils = render(
    <MerchantDefaultCategoryDialog
      open
      merchantName="Spotify"
      oldCategoryName="Subscriptions"
      newCategoryName="Music"
      onApply={onApply}
      onSkip={onSkip}
      onCancel={onCancel}
      {...overrides}
    />
  );
  return { ...utils, onApply, onSkip, onCancel };
}

describe("<MerchantDefaultCategoryDialog />", () => {
  it("renders no dialog when open=false", () => {
    render(
      <MerchantDefaultCategoryDialog
        open={false}
        merchantName="Spotify"
        oldCategoryName={null}
        newCategoryName="Music"
        onApply={() => {}}
        onSkip={() => {}}
        onCancel={() => {}}
      />
    );
    expect(screen.queryByRole("dialog")).toBeNull();
  });

  it("uses the 'no default category yet' copy when oldCategoryName is null", () => {
    renderDialog({
      oldCategoryName: null,
      newCategoryName: "Music",
    });
    const dialog = screen.getByRole("dialog");
    const text = dialog.textContent ?? "";
    expect(text).toContain("Spotify");
    expect(text).toContain("no default category yet");
    expect(text).toContain("Music");
  });

  it("mentions both old and new category names when both are present", () => {
    renderDialog({
      oldCategoryName: "Subscriptions",
      newCategoryName: "Music",
    });
    const dialog = screen.getByRole("dialog");
    const text = dialog.textContent ?? "";
    expect(text).toContain("Subscriptions");
    expect(text).toContain("Music");
    // The 'changing from … to …' branch — sanity check we hit it.
    expect(text).toContain("changing from");
  });

  it("clicking 'Apply to existing & future' calls onApply (and not onSkip)", () => {
    const { onApply, onSkip, onCancel } = renderDialog();
    fireEvent.click(screen.getByText("Apply to existing & future"));
    expect(onApply).toHaveBeenCalledTimes(1);
    expect(onSkip).not.toHaveBeenCalled();
    expect(onCancel).not.toHaveBeenCalled();
  });

  it("clicking 'Only future transactions' calls onSkip", () => {
    const { onApply, onSkip, onCancel } = renderDialog();
    fireEvent.click(screen.getByText("Only future transactions"));
    expect(onSkip).toHaveBeenCalledTimes(1);
    expect(onApply).not.toHaveBeenCalled();
    expect(onCancel).not.toHaveBeenCalled();
  });

  it("Esc keypress calls onCancel", () => {
    const { onCancel } = renderDialog();
    fireEvent.keyDown(screen.getByRole("dialog"), { key: "Escape" });
    expect(onCancel).toHaveBeenCalledTimes(1);
  });
});
