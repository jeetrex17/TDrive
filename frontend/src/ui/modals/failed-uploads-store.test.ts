import { afterEach, expect, it } from 'vitest';
import { get } from 'svelte/store';
import { dismissFailedUploadReport, failedUploadReports, rememberFailedUploadReport, type FailedUploadsPayload } from './failed-uploads-store';

afterEach(() => failedUploadReports.set([]));

it('bounds retained source references and dismisses only the selected report', () => {
    const reports: FailedUploadsPayload[] = Array.from({ length: 12 }, (_, index) => ({
        paths: [`/synthetic/source-${index}`], failures: [], parentId: '', encrypt: false,
        driveId: 1, driveTitle: 'Personal',
    }));
    for (const report of reports) rememberFailedUploadReport(report);
    expect(get(failedUploadReports)).toEqual(reports.slice(2).reverse());
    dismissFailedUploadReport(reports[6]);
    expect(get(failedUploadReports)).toHaveLength(9);
    expect(get(failedUploadReports)).not.toContain(reports[6]);
    expect(get(failedUploadReports)).toContain(reports[11]);
});
