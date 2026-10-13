<script lang="ts">
    import type { FailedUploadsPayload } from '../modals/failed-uploads-store';
    interface Props {
        reports: FailedUploadsPayload[];
        onReview: (report: FailedUploadsPayload) => void;
        onDismiss: (report: FailedUploadsPayload) => void;
    }
    let { reports, onReview, onDismiss }: Props = $props();
</script>

{#if reports.length}
    <section aria-label="Upload failures" class="failed-upload-reports">
        <h3>Files needing attention</h3>
        {#each reports as report (report)}
            <div class="failed-upload-report">
                <p>{report.failures.length} {report.failures.length === 1 ? 'file' : 'files'} in {report.driveTitle}</p>
                <div class="failed-upload-actions">
                    <button class="secondary-btn" type="button" onclick={() => onReview(report)}>Review failed files</button>
                    <button class="secondary-btn" type="button" aria-label={`Dismiss failed files in ${report.driveTitle}`} onclick={() => onDismiss(report)}>Dismiss</button>
                </div>
            </div>
        {/each}
    </section>
{/if}

<style>
    .failed-upload-reports { padding: var(--space-3); }
    h3 { font-size: var(--font-size-sm); margin: 0 0 var(--space-2); }
    .failed-upload-report + .failed-upload-report { margin-top: var(--space-3); }
    p { margin: 0 0 var(--space-2); overflow-wrap: anywhere; }
    .failed-upload-actions { display: flex; flex-wrap: wrap; gap: var(--space-2); }
</style>
