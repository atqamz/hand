import java.util.Properties
import javax.inject.Inject
import org.gradle.api.DefaultTask
import org.gradle.api.file.ConfigurableFileCollection
import org.gradle.api.file.DirectoryProperty
import org.gradle.api.provider.Property
import org.gradle.api.tasks.Input
import org.gradle.api.tasks.Internal
import org.gradle.api.tasks.OutputDirectory
import org.gradle.api.tasks.TaskAction
import org.gradle.process.ExecOperations
import org.jetbrains.kotlin.gradle.dsl.JvmTarget

plugins {
    id("com.android.application")
    id("org.jetbrains.kotlin.android")
    id("org.jetbrains.kotlin.plugin.serialization")
    id("org.jetbrains.kotlin.plugin.compose")
    id("app.cash.paparazzi")
}

val version = Properties().apply {
    file("../../../../version.properties").inputStream().use { load(it) }
}

val rustTarget = providers.gradleProperty("rustTarget").getOrElse("aarch64")
val (abiName, targetTriple) = when (rustTarget) {
    "aarch64" -> "arm64-v8a" to "aarch64-linux-android"
    "x86_64" -> "x86_64" to "x86_64-linux-android"
    else -> error("rustTarget is aarch64 or x86_64, not $rustTarget")
}
val minSdkVersion = 24

android {
    compileSdk = 37
    namespace = "io.github.atqamz.handcompanion"
    defaultConfig {
        applicationId = "io.github.atqamz.handcompanion"
        minSdk = minSdkVersion
        targetSdk = 37
        versionCode = version.getProperty("versionCode").toInt()
        versionName = version.getProperty("version")
    }
    signingConfigs {
        getByName("debug") {
            storeFile = file("debug.keystore")
            storePassword = "android"
            keyAlias = "androiddebugkey"
            keyPassword = "android"
        }
    }
    buildTypes {
        getByName("debug") {
            signingConfig = signingConfigs.getByName("debug")
            isDebuggable = true
            isJniDebuggable = true
            isMinifyEnabled = false
            packaging {
                jniLibs.keepDebugSymbols.add("*/arm64-v8a/*.so")
                jniLibs.keepDebugSymbols.add("*/x86_64/*.so")
            }
        }
        getByName("release") {
            optimization {
               enable = true
            }
            proguardFiles(
                *fileTree(".") {
                  include("**/*.pro")
                  exclude("build/**")
                }.files.toTypedArray()
            )
        }
    }
    flavorDimensions += "abi"
    productFlavors {
        create("universal") {
            dimension = "abi"
            ndk {
                abiFilters += abiName
            }
        }
    }
    compileOptions {
        sourceCompatibility = JavaVersion.VERSION_11
        targetCompatibility = JavaVersion.VERSION_11
    }
    buildFeatures {
        buildConfig = true
    }
    testOptions {
        unitTests.all { it.systemProperty("user.timezone", "UTC") }
    }
}

kotlin {
    compilerOptions {
        jvmTarget = JvmTarget.JVM_11
    }
}

val cargoWorkspace = rootProject.file("../../../")
val ndkHome = System.getenv("NDK_HOME")
    ?: System.getenv("ANDROID_NDK_HOME")
    ?: System.getenv("ANDROID_NDK_ROOT")
    ?: throw GradleException("Set NDK_HOME to the Android NDK")

val cargoDebug = tasks.register<CargoBuild>("cargoDebug") {
    group = "rust"
    description = "Builds libhand_android_lib.so for $targetTriple"
    rustTriple.set(targetTriple)
    rustAbi.set(abiName)
    release.set(false)
    ndk.set(ndkHome)
    minSdk.set(minSdkVersion)
    workspace.set(cargoWorkspace)
    rustSources.from(
        cargoWorkspace.resolve("Cargo.toml"),
        cargoWorkspace.resolve("Cargo.lock"),
        cargoWorkspace.resolve("rust-toolchain.toml"),
        cargoWorkspace.resolve("core/src"),
        cargoWorkspace.resolve("src-tauri/src")
    )
    outputDirectory.set(layout.buildDirectory.dir("rustJniLibs/debug"))
}

val cargoRelease = tasks.register<CargoBuild>("cargoRelease") {
    group = "rust"
    description = "Builds libhand_android_lib.so for $targetTriple"
    rustTriple.set(targetTriple)
    rustAbi.set(abiName)
    release.set(true)
    ndk.set(ndkHome)
    minSdk.set(minSdkVersion)
    workspace.set(cargoWorkspace)
    rustSources.from(
        cargoWorkspace.resolve("Cargo.toml"),
        cargoWorkspace.resolve("Cargo.lock"),
        cargoWorkspace.resolve("rust-toolchain.toml"),
        cargoWorkspace.resolve("core/src"),
        cargoWorkspace.resolve("src-tauri/src")
    )
    outputDirectory.set(layout.buildDirectory.dir("rustJniLibs/release"))
}

androidComponents {
    onVariants { variant ->
        val build = if (variant.buildType == "release") cargoRelease else cargoDebug
        variant.sources.jniLibs?.addGeneratedSourceDirectory(build) { it.outputDirectory }
    }
}

dependencies {
    implementation("androidx.appcompat:appcompat:1.8.0")
    implementation("androidx.activity:activity-ktx:1.10.1")
    implementation("com.google.android.material:material:1.14.0")
    implementation("androidx.lifecycle:lifecycle-process:2.10.0")
    implementation("androidx.lifecycle:lifecycle-runtime-compose:2.10.0")
    implementation("androidx.activity:activity-compose:1.10.1")
    implementation(platform("androidx.compose:compose-bom:2026.09.00"))
    implementation("androidx.compose.material3:material3")
    implementation("org.jetbrains.kotlinx:kotlinx-serialization-json:1.9.0")
    implementation("com.google.zxing:core:3.5.4")
    implementation("com.journeyapps:zxing-android-embedded:4.3.0") {
        exclude(group = "com.google.zxing", module = "core")
    }
    testImplementation("junit:junit:4.13.2")
    androidTestImplementation("androidx.test.ext:junit:1.1.4")
    androidTestImplementation("androidx.test.espresso:espresso-core:3.5.0")
}

abstract class CargoBuild : DefaultTask() {
    @get:Input abstract val rustTriple: Property<String>
    @get:Input abstract val rustAbi: Property<String>
    @get:Input abstract val release: Property<Boolean>
    @get:Input abstract val ndk: Property<String>
    @get:Input abstract val minSdk: Property<Int>
    @get:InputFiles abstract val rustSources: ConfigurableFileCollection
    @get:Internal abstract val workspace: DirectoryProperty
    @get:OutputDirectory abstract val outputDirectory: DirectoryProperty
    @get:Inject abstract val exec: ExecOperations

    @TaskAction
    fun build() {
        val root = workspace.get().asFile
        val triple = rustTriple.get()
        val host = when {
            System.getProperty("os.name").startsWith("Windows") -> "windows-x86_64"
            System.getProperty("os.name") == "Mac OS X" -> "darwin-x86_64"
            else -> "linux-x86_64"
        }
        val toolchain = "${ndk.get()}/toolchains/llvm/prebuilt/$host/bin"
        val cc = "$toolchain/$triple${minSdk.get()}-clang"
        val suffix = triple.replace('-', '_')
        val arguments = mutableListOf("cargo", "build", "--locked", "--target", triple)
        if (release.get()) arguments += "--release"
        exec.exec {
            workingDir(root)
            commandLine(*arguments.toTypedArray())
            environment("CC_$suffix", cc)
            environment("AR_$suffix", "$toolchain/llvm-ar")
            environment("CARGO_TARGET_${triple.uppercase().replace('-', '_')}_LINKER", cc)
        }
        val profile = if (release.get()) "release" else "debug"
        val built = root.resolve("target/$triple/$profile/libhand_android_lib.so")
        val out = outputDirectory.get().asFile.resolve(rustAbi.get())
        out.mkdirs()
        built.copyTo(out.resolve("libhand_android_lib.so"), overwrite = true)
    }
}
