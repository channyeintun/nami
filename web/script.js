document.addEventListener('DOMContentLoaded', () => {
    // Initialize Lucide icons
    if (typeof lucide !== 'undefined') {
        lucide.createIcons();
    }

    // Drawer Logic (Docs only)
    const menuToggle = document.getElementById("menu-toggle");
    const closeMenu = document.getElementById("close-menu");
    const mobileDrawer = document.getElementById("mobile-drawer");
    const mobileLinks = document.querySelectorAll(".mobile-link");

    if (menuToggle && closeMenu && mobileDrawer) {
        const toggleDrawer = () => mobileDrawer.classList.toggle("open");
        menuToggle.addEventListener("click", toggleDrawer);
        closeMenu.addEventListener("click", toggleDrawer);

        mobileLinks.forEach((link) => {
            link.addEventListener("click", () =>
                mobileDrawer.classList.remove("open")
            );
        });
    }

    // Scrollspy (Docs only)
    const sidebarLinks = document.querySelectorAll(".sidebar-link");
    const sections = document.querySelectorAll("section");

    if (sidebarLinks.length > 0 && sections.length > 0) {
        window.addEventListener("scroll", () => {
            let current = "";
            sections.forEach((s) => {
                const rect = s.getBoundingClientRect();
                if (window.scrollY >= s.offsetTop - 200) {
                    current = s.getAttribute("id");
                }
            });

            [...sidebarLinks, ...mobileLinks].forEach((l) => {
                l.classList.remove("active");
                if (l.getAttribute("href") === `#${current}`)
                    l.classList.add("active");
            });
        });
    }

    // lucide.createIcons() swaps each <i data-lucide> placeholder for an <svg>
    // that keeps the data-lucide attribute, so the icon has to be found by that
    // attribute each time rather than as the original <i>.
    const setCopyIcon = (button, name) => {
        const icon = button.querySelector("[data-lucide]");
        if (!icon) return;
        icon.setAttribute("data-lucide", name);
        icon.classList.toggle("text-brand", name === "check");
        if (typeof lucide !== 'undefined') {
            lucide.createIcons();
        }
    };

    // Copy functionality
    document.querySelectorAll(".copy-button").forEach((button) => {
        button.addEventListener("click", async () => {
            const container =
                button.closest(".relative") ||
                button.closest(".group") ||
                button.parentElement;
            const code = container.querySelector("code");
            const text = button.dataset.copyText || code?.innerText.trim();
            if (!text) return;

            try {
                await navigator.clipboard.writeText(text);

                setCopyIcon(button, "check");
                setTimeout(() => setCopyIcon(button, "copy"), 2000);
            } catch (err) {
                console.error("Failed to copy: ", err);
            }
        });
    });
});
