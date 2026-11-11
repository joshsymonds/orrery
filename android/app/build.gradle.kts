plugins {
    id("com.android.application")
    id("org.jetbrains.kotlin.plugin.compose")
}

android {
    namespace = "io.github.joshsymonds.orrery"
    compileSdk {
        version = release(37) {
            minorApiLevel = 0
        }
    }
    buildToolsVersion = "36.0.0"

    defaultConfig {
        applicationId = "io.github.joshsymonds.orrery"
        minSdk = 34
        targetSdk = 36
        versionCode = 1
        versionName = "0.1.0"
    }

    buildFeatures {
        compose = true
    }

    compileOptions {
        sourceCompatibility = JavaVersion.VERSION_17
        targetCompatibility = JavaVersion.VERSION_17
    }

    testOptions {
        unitTests.isIncludeAndroidResources = true
    }

    lint {
        // A lint error fails `gradle lintDebug`, in CI and locally. Warnings
        // do not fail the build but are printed, so they stay visible instead
        // of hiding in an HTML report nobody opens.
        abortOnError = true
        warningsAsErrors = false
        textReport = true
        htmlReport = false
    }
}

// Debug builds only: no release variant exists, so no unsigned or signed
// release APK can be produced from this tree.
androidComponents {
    beforeVariants(selector().withBuildType("release")) { variant ->
        variant.enable = false
    }
}

dependencies {
    implementation("androidx.activity:activity-compose:1.10.1")
    implementation("com.connectrpc:connect-kotlin-okhttp:0.10.0")
    implementation("com.connectrpc:connect-kotlin-google-javalite-ext:0.10.0")
    implementation("com.google.protobuf:protobuf-javalite:4.36.2")
    implementation("com.squareup.okhttp3:okhttp:5.5.0")
    implementation(platform("androidx.compose:compose-bom:2025.04.01"))
    implementation("androidx.compose.material3:material3")
    implementation("androidx.compose.ui:ui")
    implementation("org.unifiedpush.android:connector:3.0.10")

    testImplementation(platform("androidx.compose:compose-bom:2025.04.01"))
    testImplementation("androidx.compose.ui:ui-test-junit4")
    testImplementation("androidx.test:core:1.6.1")
    testImplementation("junit:junit:4.13.2")
    testImplementation("org.json:json:20240303")
    testImplementation("org.robolectric:robolectric:4.14.1")

    debugImplementation(platform("androidx.compose:compose-bom:2025.04.01"))
    debugImplementation("androidx.compose.ui:ui-test-manifest")
}

tasks.withType<org.jetbrains.kotlin.gradle.tasks.KotlinCompile>().configureEach {
    if (name.contains("UnitTest")) {
        compilerOptions.noJdk.set(false)
        compilerOptions.freeCompilerArgs.add("-Xadd-modules=jdk.httpserver")
    }
}

tasks.withType<org.gradle.api.tasks.testing.Test>().configureEach {
    jvmArgs("--add-modules", "jdk.httpserver")
}
