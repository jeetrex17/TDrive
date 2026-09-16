<script lang="ts">
    /**
     * TDrive's mark: a closed padlock, drawn rather than shipped as an image.
     *
     * It replaces a raster Telegram paper-plane-in-a-cloud that used to stand
     * in for the app. That was wrong three times over -- it was another
     * company's trademark being used as this product's identity, it was a
     * gradient, and it was a vendor blue, all of which the design direction
     * rules out. It also said the wrong thing: TDrive stores files in Telegram,
     * it is not Telegram.
     *
     * Being a stroked path means it takes the theme's colour, stays flat at any
     * size, and carries the same stroke weight as the app's other glyphs, so it
     * reads as part of one family rather than a logo bolted on. The keyhole is
     * deliberately the same promise the encrypted-file badge makes on a row.
     *
     * This is an honest placeholder, not a considered wordmark -- picking the
     * real one is a branding decision that wants a person.
     */
    interface Props {
        size?: number;
        /** Extra class so each host can place and colour it. */
        class?: string;
    }

    let { size = 32, class: className = '' }: Props = $props();
</script>

<svg
    class={`brand-mark ${className}`}
    width={size}
    height={size}
    viewBox="0 0 32 32"
    fill="none"
    stroke="currentColor"
    stroke-width="2"
    stroke-linecap="round"
    stroke-linejoin="round"
    aria-hidden="true"
>
    <rect x="4" y="9" width="24" height="19" rx="3" />
    <path d="M11 9V6.5a5 5 0 0 1 10 0V9" />
    <circle cx="16" cy="17" r="2.25" />
    <path d="M16 19.25V22" />
</svg>
