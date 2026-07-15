import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import { AuthDialog } from "./AuthDialog";

const register = vi.fn();
const login = vi.fn();

vi.mock("./AuthContext", () => ({
  useAuth: () => ({ register, login }),
}));

describe("AuthDialog", () => {
  it("submits the registration fields through the auth context", async () => {
    register.mockResolvedValue(undefined);
    const onClose = vi.fn();
    render(<AuthDialog open onClose={onClose} />);

    fireEvent.click(screen.getByRole("button", { name: "注册" }));
    fireEvent.change(screen.getByLabelText("用户名"), { target: { value: "new_user" } });
    fireEvent.change(screen.getByLabelText("昵称"), { target: { value: "新用户" } });
    fireEvent.change(screen.getByLabelText("密码"), { target: { value: "password_123" } });
    fireEvent.click(screen.getByRole("button", { name: "注册并登录" }));

    await waitFor(() => expect(register).toHaveBeenCalledWith("new_user", "password_123", "新用户"));
    expect(onClose).toHaveBeenCalledOnce();
  });
});
