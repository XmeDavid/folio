import { describe, it, expect, vi } from "vitest";
import { render, fireEvent } from "@testing-library/react";
import { MerchantDefaultCategoryDialog } from "@/components/classification/merchant-default-category-dialog";

// Specs for the cascade-confirmation dialog. Covers:
//   - hidden when open=false
//   - copy variant when oldCategoryName is null (first-time default)
//   - copy variant when both old + new are set
//   - apply / skip / cancel callbacks (button + backdrop + Esc)

function renderDialog(overrides: Partial<React.ComponentProps<typeof MerchantDefaultCategoryDialog>> = {}) {
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
  it("renders nothing when open=false", () => {
    const { container } = render(
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
    // Component returns null; container should have no child nodes.
    expect(container.firstChild).toBeNull();
  });

  it("uses the 'no default category yet' copy when oldCategoryName is null", () => {
    const { getByRole } = renderDialog({
      oldCategoryName: null,
      newCategoryName: "Music",
    });
    const dialog = getByRole("dialog");
    const text = dialog.textContent ?? "";
    expect(text).toContain("Spotify");
    expect(text).toContain("no default category yet");
    expect(text).toContain("Music");
  });

  it("mentions both old and new category names when both are present", () => {
    const { getByRole } = renderDialog({
      oldCategoryName: "Subscriptions",
      newCategoryName: "Music",
    });
    const dialog = getByRole("dialog");
    const text = dialog.textContent ?? "";
    expect(text).toContain("Subscriptions");
    expect(text).toContain("Music");
    // The 'changing from … to …' branch — sanity check we hit it.
    expect(text).toContain("changing from");
  });

  it("clicking 'Apply to existing & future' calls onApply (and not onSkip)", () => {
    const { getByText, onApply, onSkip, onCancel } = renderDialog();
    fireEvent.click(getByText("Apply to existing & future"));
    expect(onApply).toHaveBeenCalledTimes(1);
    expect(onSkip).not.toHaveBeenCalled();
    expect(onCancel).not.toHaveBeenCalled();
  });

  it("clicking 'Only future transactions' calls onSkip", () => {
    const { getByText, onApply, onSkip, onCancel } = renderDialog();
    fireEvent.click(getByText("Only future transactions"));
    expect(onSkip).toHaveBeenCalledTimes(1);
    expect(onApply).not.toHaveBeenCalled();
    expect(onCancel).not.toHaveBeenCalled();
  });

  it("clicking the backdrop calls onCancel", () => {
    const { container, onCancel } = renderDialog();
    // The outermost rendered node is the backdrop (role="presentation").
    const backdrop = container.querySelector('[role="presentation"]');
    expect(backdrop).not.toBeNull();
    // Synthesize a click whose target === currentTarget so the handler triggers.
    fireEvent.click(backdrop as Element);
    expect(onCancel).toHaveBeenCalledTimes(1);
  });

  it("Esc keypress calls onCancel", () => {
    const { onCancel } = renderDialog();
    fireEvent.keyDown(document, { key: "Escape" });
    expect(onCancel).toHaveBeenCalledTimes(1);
  });
});
