#ifndef TDRIVE_BIOMETRIC_APPLE_H
#define TDRIVE_BIOMETRIC_APPLE_H

#include <stddef.h>

int tdrive_bio_status(const char *account, int *enrolled, int *kind);
void *tdrive_bio_context_create(void);
void tdrive_bio_context_cancel(void *handle);
void tdrive_bio_context_release(void *handle);
int tdrive_bio_enroll(void *handle, const char *account, const unsigned char *secret, size_t size);
int tdrive_bio_unlock(void *handle, const char *account, unsigned char **secret, size_t *size);
int tdrive_bio_delete(const char *account);
void tdrive_bio_secret_free(unsigned char *secret, size_t size);

#endif
