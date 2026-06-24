import { mount, unmount, type Component } from 'svelte';

export type SvelteMountOptions<Props extends Record<string, unknown>> = {
    target: Document | Element | ShadowRoot;
    props: Props;
    intro?: boolean;
};

export type SvelteMountHandle<Exports extends Record<string, unknown>> = {
    instance: Exports;
    destroy(options?: { outro?: boolean }): Promise<void>;
};

export function mountSvelte<
    Props extends Record<string, unknown>,
    Exports extends Record<string, unknown> = Record<string, never>,
>(
    component: Component<Props, Exports>,
    { target, props, intro = false }: SvelteMountOptions<Props>,
): SvelteMountHandle<Exports> {
    const instance = mount(component, { target, props, intro });
    let destroyed = false;

    return {
        instance,
        async destroy(options = {}) {
            if (destroyed) return;
            destroyed = true;
            await unmount(instance, { outro: options.outro ?? false });
        },
    };
}
